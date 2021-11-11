# pero_ocr
Simple CLI util for running OCR on images through PERO OCR API

Usage:

```bash
Usage of batch_pero_ocr:
  -c string
        cancel request with given id
  -d string
        dir to ocr in-place
  -e int
        engine id for use in ocr process (default 1)
  -engines
        ask ocr server for available engines information
  -pull-only string
        only download alto + txt for given request id
```

## Config example

Config is stored in users $HOME directory as `.ocrtools.yml`.
When no config is available in invoking directory or home dir, new config templated is created in `$HOME/.ocrtools.yml`.

```yaml
pero:
  api_key: api-key-here
  default_engine: 1
  endpoint: https://pero-ocr.fit.vutbr.cz/api/
```

Please mind, that **trailing slash "/" in endpoint is mandatory**.

## Creating OCR

```bash
./pero_ocr -d <dir with images>
```

While ocr is running every event is logged into `ocr_log.txt` located in directory with images.
Request ID is recorded on the begining of log, which may be used later for canceling ocr request or additional download of OCR and ALTO files.
Default selected engine is engine with id 1, which should be: `czech_old_printed`, engine with id 2 should be: `czech_old_handwritten`.

Engine selection is done by `-e <engine ID>` switch in combination with `-d` switch.

All available engines can be printout by `./pero_ocr -engines`.

## Cancel OCR request

```bash
./pero_ocr -c <request id>
```

## Additional download OCR and ALTO

Downloads OCR and ALTO from given request id into the target directory.

```bash
./pero_ocr -pull-only <request id> -d <dir with images>
```

# How to build
```bash
$ go build
```