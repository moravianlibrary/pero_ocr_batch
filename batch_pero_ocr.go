package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"image/jpeg"
	"io"
	"io/fs"
	"io/ioutil"
	"log"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
	"unicode"

	"github.com/spf13/viper"
	"golang.org/x/image/tiff"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

const (
	httpTimeoutDuration = 30 * time.Minute
)

var (
	imgExts  = [...]string{".tiff", ".tif", ".jpg", ".png", ".jp2", ".jp2k"}
	engineID *int
	apiKey   = ""
	endpoint = "https://pero-ocr.fit.vutbr.cz/api/"
	version  = "unknown"
)

func main() {
	userHome, err := os.UserHomeDir()
	if err != nil {
		log.Fatalln("Error user home directory cannot be found!")
	}
	// Viper setup
	viper.SetConfigName(".ocrtools")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(userHome)
	viper.AddConfigPath(".")

	// Default config
	viper.SetDefault("pero.api_key", "api-key-here")
	viper.SetDefault("pero.endpoint", "https://pero-ocr.fit.vutbr.cz/api/")
	viper.SetDefault("pero.default_engine", 1)

	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			config := userHome + string(os.PathSeparator) + ".ocrtools.yml"
			viper.WriteConfigAs(config)
			log.Println("Created default config, please set it up in: " + config)
			os.Exit(0)
		} else {
			log.Fatalln("Error please check your config! " + viper.ConfigFileUsed())
		}
	}

	apiKey = viper.GetString("pero.api_key")
	endpoint = viper.GetString("pero.endpoint")

	dir := flag.String("d", "", "dir to ocr in-place")
	cancel := flag.String("c", "", "cancel request with given id")
	pullOnly := flag.String("pull-only", "", "only download alto + txt for given request id")
	engines := flag.Bool("engines", false, "ask ocr server for available engines information")
	engineID = flag.Int("e", viper.GetInt("pero.default_engine"), "engine id for use in ocr process")
	ver := flag.Bool("version", false, "get util version")

	flag.Parse()
	if *ver {
		fmt.Println(version)
		os.Exit(0)
	}
	if *cancel != "" {
		cancelOcrRequest(*cancel)
		os.Exit(0)
	}
	if *engines {
		printAvailableEngines()
		os.Exit(0)
	}
	if *dir == "" {
		fmt.Fprintln(os.Stderr, "-d switch is mandatory")
		flag.Usage()
		os.Exit(1)
	}

	if !isDir(*dir) {
		fmt.Fprintln(os.Stderr, "file is not a directory or does not exist")
		os.Exit(2)
	}

	// run through all available image files
	var files []string
	err = filepath.WalkDir(*dir, func(path string, d fs.DirEntry, err error) error {
		if !d.IsDir() && isImage(path) {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "error while examining directory, check permissions and contents of this directory")
		os.Exit(3)
	}

	// setup loging to stdout and file
	logFile, err := os.OpenFile(path.Join(*dir, "ocr_log.txt"), os.O_CREATE|os.O_APPEND|os.O_RDWR, 0666)
	if err != nil {
		panic(err)
	}
	mw := io.MultiWriter(os.Stdout, logFile)
	log.SetOutput(mw)

	// download only and exit
	if *pullOnly != "" {
		log.Println("starting requested standalone ocr+alto download")
		downloadOcrAlto(files, *pullOnly)
		log.Println("standalone download done")
		os.Exit(0)
	}

	// run ocr
	requestId := postPreprocessRequest(files)
	if requestId == "" {
		fmt.Fprintln(os.Stderr, "Error for post_preprocess_request unknown response")
		os.Exit(5)
	}

	log.Printf("OCR for %s RequestId %s\n", *dir, requestId)

	// FIXME: check if everything is in state CREATED then upload data
	time.Sleep(1 * time.Second)
	log.Println("starting file upload...")
	uploadedFiles := uploadFilesForOcr(files, requestId)

	log.Println("waiting for ocr to be done...")
	waitForOcrFinish(uploadedFiles, requestId)

	log.Println("downloading files from ocr...")
	downloadOcrAlto(uploadedFiles, requestId)
	log.Println("batch complete")
}

func cancelOcrRequest(id string) {
	req, _ := http.NewRequest("POST", endpoint+"cancel_request/"+id, nil)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Add("api-key", apiKey)
	client := http.Client{}
	res, err := client.Do(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error creating request for post_preprocess_request")
		os.Exit(1)
	}
	if res.StatusCode == http.StatusOK {
		fmt.Println("OK cancel req: " + id)
		return
	}
	fmt.Fprintf(os.Stderr, "Error cancel request: %s server returned status %d\n", id, res.StatusCode)
}

func downloadOcrAlto(files []string, requestId string) {
	for _, file := range files {
		req, _ := http.NewRequest("GET", endpoint+"download_results/"+requestId+"/"+ocrFilename(file)+"/txt", nil)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Add("api-key", apiKey)
		client := http.Client{}
		res, err := client.Do(req)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error creating request for request_status")
			os.Exit(1)
		}
		if res.StatusCode == http.StatusOK {
			filename := strings.TrimSuffix(file, filepath.Ext(file)) + ".txt"
			txt, err := os.OpenFile(filename, os.O_CREATE|os.O_RDWR, 0775)
			if err != nil {
				fmt.Fprintln(os.Stderr, "error creating file: "+filename)
				continue
			}
			b, _ := ioutil.ReadAll(res.Body)
			txt.Write(b)
			txt.Close()
		}
		req, _ = http.NewRequest("GET", endpoint+"download_results/"+requestId+"/"+ocrFilename(file)+"/alto", nil)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Add("api-key", apiKey)
		client = http.Client{}
		res, err = client.Do(req)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error creating request for request_status")
			os.Exit(1)
		}
		if res.StatusCode == http.StatusOK {
			filename := strings.TrimSuffix(file, filepath.Ext(file)) + ".xml"
			txt, err := os.OpenFile(filename, os.O_CREATE|os.O_RDWR, 0775)
			if err != nil {
				fmt.Fprintln(os.Stderr, "error creating file: "+filename)
				continue
			}
			b, _ := ioutil.ReadAll(res.Body)
			txt.Write(b)
			txt.Close()
		}
	}
}

func isDir(pathFile string) bool {
	if pathAbs, err := filepath.Abs(pathFile); err != nil {
		return false
	} else if fileInfo, err := os.Stat(pathAbs); os.IsNotExist(err) || !fileInfo.IsDir() {
		return false
	}

	return true
}

// isImage checks if file extension is on the list
//
// If supported image extension is found returns true
func isImage(file string) bool {
	ext := filepath.Ext(file)
	for _, imgExt := range imgExts {
		if imgExt == ext {
			return true
		}
	}
	return false
}

// postPreprocessRequest prepares for upload images to ocr
//
// returns request id
func postPreprocessRequest(files []string) string {
	var data = make(map[string]interface{})
	data["engine"] = *engineID
	var imgs = make(map[string]interface{})
	for _, file := range files {
		imgs[ocrFilename(file)] = nil
	}
	data["images"] = imgs
	j, err := json.Marshal(data)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error marshaling data for post_preprocess_request")
		os.Exit(5)
	}
	req, _ := http.NewRequest("POST", endpoint+"post_processing_request", bytes.NewReader(j))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Add("api-key", apiKey)
	client := http.Client{}
	res, err := client.Do(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error creating request for post_preprocess_request")
		os.Exit(1)
	}
	if res.StatusCode != http.StatusOK {
		switch res.StatusCode {
		case 404:
			fmt.Fprintln(os.Stderr, "Error for post_preprocess_request server responded 404 - ocr engine not found")
			os.Exit(6)
		case 422:
			fmt.Fprintln(os.Stderr, "Error for post_preprocess_request server responded 422 - bad json data")
			os.Exit(6)
		default:
			fmt.Fprintln(os.Stderr, "Error for post_preprocess_request server responded "+strconv.Itoa(res.StatusCode)+" (this error is not implemented)")
			os.Exit(6)
		}
	}

	var respData map[string]interface{}
	err = json.NewDecoder(res.Body).Decode(&respData)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error for post_preprocess_request server not responded using json, cannot continue")
		os.Exit(6)
	}

	if respData["status"] == "success" {
		return respData["request_id"].(string)
	}

	return ""
}

// uploadFilesForOcr is self explanatory :P
//
// RequestID returned by PERO server is needed
func uploadFilesForOcr(files []string, requestId string) []string {
	c := http.Client{}
	var uploadedFiles []string
	for _, file := range files {
		f := convertTiffToJpg(file)
		if f == "" {
			f = file
		}
		b, w := createMultipartFormData("file", f)
		req, _ := http.NewRequest("POST", endpoint+"upload_image/"+requestId+"/"+ocrFilename(file), &b)
		req.Header.Set("Content-Type", w.FormDataContentType())
		req.Header.Add("api-key", apiKey)
		res, err := c.Do(req)
		if file != f {
			// remove temporary file
			os.Remove(f)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error sending file: %s to PERO\n", file)
			continue
		}
		if res.StatusCode != http.StatusOK {
			var respData map[string]interface{}
			json.NewDecoder(res.Body).Decode(&respData)
			fmt.Fprintf(os.Stderr, "Error server status code %d with message: %s\n", res.StatusCode, respData["message"])
		} else {
			log.Printf("OK upload: %s\n", file)
			uploadedFiles = append(uploadedFiles, file)
		}
		w.Close()
		res.Body.Close()
		runtime.GC()
	}
	return uploadedFiles
}

func isMn(r rune) bool {
	return unicode.Is(unicode.Mn, r) // Mn: nonspacing marks
}

func normalizeString(s string) string {
	t := transform.Chain(norm.NFD, transform.RemoveFunc(isMn), norm.NFC)
	result, _, _ := transform.String(t, s)
	return result
}

func ocrFilename(file string) string {
	return normalizeString(strings.ReplaceAll(filepath.Base(file), " ", ""))
}

func createMultipartFormData(fieldName, fileName string) (bytes.Buffer, *multipart.Writer) {
	var b bytes.Buffer
	var err error
	w := multipart.NewWriter(&b)
	var fw io.Writer
	file := mustOpen(fileName)
	if fw, err = w.CreateFormFile(fieldName, file.Name()); err != nil {
		fmt.Errorf("Error creating writer: %v", err)
	}
	if _, err = io.Copy(fw, file); err != nil {
		fmt.Errorf("Error with io.Copy: %v", err)
	}
	w.Close()
	file.Close()
	return b, w
}

func waitForOcrFinish(files []string, requestId string) {
	var done bool
	var doneCount int
	for {
		done = true
		doneCount = 0
		req, _ := http.NewRequest("GET", endpoint+"request_status/"+requestId, nil)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Add("api-key", apiKey)
		client := http.Client{Timeout: httpTimeoutDuration} //nolint:exhaustivestruct
		res, err := client.Do(req)
		if err != nil {
			if err.(*url.Error).Timeout() {
				fmt.Fprintln(os.Stderr, "Error connection timeout for request_status")
			} else {
				fmt.Fprintln(os.Stderr, "Error creating request for request_status")
			}
			os.Exit(1)
		}
		if res.StatusCode != http.StatusOK {
			switch res.StatusCode {
			case 404:
				fmt.Fprintln(os.Stderr, "Error for request_status server responded 404 - Request doesn't exist.")
				os.Exit(6)
			case 401:
				fmt.Fprintln(os.Stderr, "Error for request_status server responded 422 - Request doesn't belong to this API key.")
				os.Exit(6)
			default:
				fmt.Fprintln(os.Stderr, "Error for request_status server responded "+strconv.Itoa(res.StatusCode)+" (this error is not implemented)")
				os.Exit(6)
			}
		}

		var respData map[string]interface{}
		err = json.NewDecoder(res.Body).Decode(&respData)
		if err != nil {
			fmt.Println("Error decoding status response")
			os.Exit(7)
		}
		res.Body.Close()
		client.CloseIdleConnections()
		for _, file := range files {
			var img map[string]interface{}
			if _, ok := respData["request_status"]; ok {
				img = respData["request_status"].(map[string]interface{})
				img = img[ocrFilename(file)].(map[string]interface{})
				if img["state"] == "PROCESSED" {
					doneCount += 1
					continue
				} else {
					done = false
				}
			}
		}
		if doneCount != len(files) {
			log.Printf("OCRs are not done yet (%d / %d), trying again in 60 seconds...\n", doneCount, len(files))
			time.Sleep(60 * time.Second)
		} else {
			log.Printf("OCRs are not done yet (%d / %d), trying again in 60 seconds...\n", doneCount, len(files))
			log.Println("OCRs are done!")
		}

		if done {
			break
		}
	}
}

func mustOpen(f string) *os.File {
	r, err := os.Open(f)
	if err != nil {
		panic(err)
	}
	return r
}

// convertTiffToJpg
//
// if tiff convert to jpg
// if something else, not modified path is returned
func convertTiffToJpg(file string) string {
	ext := filepath.Ext(file)
	if strings.ToLower(ext) == ".tif" || strings.ToLower(ext) == ".tiff" {
		f, err := os.Open(file)
		if err != nil {
			fmt.Println("Couldn't open file: " + file + " skipping...")
			return ""
		}
		decode, err := tiff.Decode(f)
		if err != nil {
			fmt.Println("Couldn't read TIFF file: " + file + " skipping...")
			f.Close()
			return ""
		}
		jpgFile, err := os.Create(strings.TrimSuffix(file, ext) + ".jpg")
		if err != nil {
			f.Close()
			fmt.Println("Cannot create output file skipping...")
			return ""
		}
		err = jpeg.Encode(jpgFile, decode, nil)
		if err != nil {
			fmt.Println("Error encoding file from decode to jpg!")
			return ""
		}

		f.Close()
		jpgFile.Close()
		return strings.TrimSuffix(file, ext) + ".jpg"
	}
	return file
}

func printAvailableEngines() {
	req, _ := http.NewRequest("GET", endpoint+"get_engines", nil)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Add("api-key", apiKey)
	client := http.Client{}
	res, err := client.Do(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error creating request for get_engines")
		os.Exit(1)
	}
	if res.StatusCode == http.StatusOK {
		var respData map[string]interface{}
		err = json.NewDecoder(res.Body).Decode(&respData)
		if err != nil {
			fmt.Println("Error decoding engines response")
			os.Exit(7)
		}
		res.Body.Close()
		var engines map[string]interface{}
		engines = respData["engines"].(map[string]interface{})

		if respData["status"] == "success" {
			w := tabwriter.NewWriter(os.Stdout, 1, 1, 1, ' ', 0)
			fmt.Fprintln(w, "id\tname\tdescription\t")

			for name, engineInfo := range engines {
				var engineData map[string]interface{}
				engineData = engineInfo.(map[string]interface{})
				if engineData["description"] == nil {
					fmt.Fprintf(w, "%d\t%s\t%s\t\n", int64(engineData["id"].(float64)), name, "N/A")
					continue
				}
				fmt.Fprintf(w, "%d\t%s\t%s\t\n", int64(engineData["id"].(float64)), name, engineData["description"])
			}
			w.Flush()
		}
	}
}
