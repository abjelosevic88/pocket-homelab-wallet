package server

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"  // photos of a card
	_ "image/jpeg" // photos of a card
	_ "image/png"  // photos of a card
	"net/http"
	"strings"
	"unicode"

	"github.com/boombuler/barcode"
	"github.com/boombuler/barcode/aztec"
	"github.com/boombuler/barcode/codabar"
	"github.com/boombuler/barcode/code128"
	"github.com/boombuler/barcode/code39"
	"github.com/boombuler/barcode/code93"
	"github.com/boombuler/barcode/datamatrix"
	"github.com/boombuler/barcode/ean"
	"github.com/boombuler/barcode/pdf417"
	"github.com/boombuler/barcode/qr"
	"github.com/boombuler/barcode/twooffive"
	"github.com/makiuchi-d/gozxing"
	zaztec "github.com/makiuchi-d/gozxing/aztec"
	zdatamatrix "github.com/makiuchi-d/gozxing/datamatrix"
	"github.com/makiuchi-d/gozxing/oned"
	zqrcode "github.com/makiuchi-d/gozxing/qrcode"
)

// ---- barcodes: drawn on the server as SVG, read from photos ----
//
// The code is drawn here (not in the browser) so every client — the app,
// Pocket Travel's trip page, Pocket AI — shows the same crisp code from one
// URL. Photos are read here too, because iPhones have no barcode reader in
// the browser.

// codeFormat is a kind of barcode a card can carry.
type codeFormat struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	TwoD  bool   `json:"two_d"`
	Hint  string `json:"hint"`
}

var codeFormats = []codeFormat{
	{"qr", "QR code", true, "Square, with three corner boxes"},
	{"code128", "Code 128", false, "Lines; letters and digits (most shop cards)"},
	{"ean13", "EAN-13", false, "Lines; 13 digits (many shop cards in Europe)"},
	{"aztec", "Aztec", true, "Square with a bull's eye in the middle (train tickets)"},
	{"pdf417", "PDF417", true, "A wide block of small lines (boarding passes, IDs)"},
	{"datamatrix", "Data Matrix", true, "A small square of dots"},
	{"ean8", "EAN-8", false, "Lines; 8 digits"},
	{"upca", "UPC-A", false, "Lines; 12 digits (shops in America)"},
	{"upce", "UPC-E", false, "Short lines; 8 digits"},
	{"code39", "Code 39", false, "Lines; capitals and digits (memberships, libraries)"},
	{"code93", "Code 93", false, "Lines; capitals and digits"},
	{"itf", "ITF", false, "Lines; an even number of digits"},
	{"codabar", "Codabar", false, "Lines; digits (libraries, blood banks)"},
}

func validFormat(f string) bool {
	if f == "" {
		return true
	}
	for _, c := range codeFormats {
		if c.Key == f {
			return true
		}
	}
	return false
}

func isTwoD(f string) bool {
	for _, c := range codeFormats {
		if c.Key == f {
			return c.TwoD
		}
	}
	return false
}

func digitsOnly(v string) bool {
	if v == "" {
		return false
	}
	for _, r := range v {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// expandUPCE turns an 8-digit UPC-E into the 12-digit UPC-A it stands for.
func expandUPCE(v string) (string, error) {
	if len(v) == 6 {
		v = "0" + v
	}
	if len(v) == 7 {
		v += "0" // check digit recomputed below
	}
	if len(v) != 8 || !digitsOnly(v) || (v[0] != '0' && v[0] != '1') {
		return "", errors.New("UPC-E is 8 digits starting with 0 or 1")
	}
	ns, d := v[0:1], v[1:7]
	var body string
	switch d[5] {
	case '0', '1', '2':
		body = d[0:2] + d[5:6] + "0000" + d[2:5]
	case '3':
		body = d[0:3] + "00000" + d[3:5]
	case '4':
		body = d[0:4] + "00000" + d[4:5]
	default:
		body = d[0:5] + "0000" + d[5:6]
	}
	return ns + body, nil // 11 digits; the EAN encoder adds the check digit
}

var errCheckDigit = errors.New("the last digit does not fit the others; check the number, or choose Code 128")

// encodeCode draws a value in a format.
func encodeCode(format, value string) (barcode.Barcode, error) {
	v := strings.TrimSpace(value)
	if v == "" {
		return nil, errors.New("there is nothing to put in the barcode")
	}
	switch format {
	case "qr":
		return qr.Encode(v, qr.M, qr.Auto)
	case "aztec":
		return aztec.Encode([]byte(v), aztec.DEFAULT_EC_PERCENT, aztec.DEFAULT_LAYERS)
	case "pdf417":
		return pdf417.Encode(v, 2)
	case "datamatrix":
		return datamatrix.Encode(v)
	case "code128":
		return code128.Encode(v)
	case "ean13":
		if !digitsOnly(v) || (len(v) != 12 && len(v) != 13) {
			return nil, errors.New("EAN-13 is 13 digits")
		}
		if len(v) == 13 && !eanCheck(v) {
			return nil, errCheckDigit
		}
		return ean.Encode(v)
	case "ean8":
		if !digitsOnly(v) || (len(v) != 7 && len(v) != 8) {
			return nil, errors.New("EAN-8 is 8 digits")
		}
		if len(v) == 8 && !eanCheck(v) {
			return nil, errCheckDigit
		}
		return ean.Encode(v)
	case "upca":
		if !digitsOnly(v) || (len(v) != 11 && len(v) != 12) {
			return nil, errors.New("UPC-A is 12 digits")
		}
		if len(v) == 12 && !eanCheck("0"+v) {
			return nil, errCheckDigit
		}
		return ean.Encode("0" + v)
	case "upce":
		a, err := expandUPCE(v)
		if err != nil {
			return nil, err
		}
		return ean.Encode("0" + a)
	case "code39":
		return code39.Encode(v, false, true)
	case "code93":
		return code93.Encode(v, false, true)
	case "itf":
		if !digitsOnly(v) || len(v)%2 != 0 {
			return nil, errors.New("ITF is an even number of digits")
		}
		return twooffive.Encode(v, true)
	case "codabar":
		up := strings.ToUpper(v)
		if !strings.ContainsAny(up[:1], "ABCD") {
			up = "A" + up + "A"
		}
		return codabar.Encode(up)
	}
	return nil, fmt.Errorf("unknown barcode %q", format)
}

// codeSVG draws a barcode as SVG: black modules on white, with the quiet
// zone scanners need, crisp at any size.
func codeSVG(format, value string) ([]byte, error) {
	bc, err := encodeCode(format, value)
	if err != nil {
		return nil, err
	}
	b := bc.Bounds()
	w, h := b.Dx(), b.Dy()
	twoD := h > 1
	quiet := 10
	barH := 0
	if twoD {
		quiet = 2
		if format == "qr" {
			quiet = 4
		}
	} else {
		barH = w * 2 / 5
		if barH < 30 {
			barH = 30
		}
		if barH > 80 {
			barH = 80
		}
		h = barH
	}
	var path strings.Builder
	rows := b.Dy()
	for y := 0; y < rows; y++ {
		x := 0
		for x < w {
			if !dark(bc.At(b.Min.X+x, b.Min.Y+y)) {
				x++
				continue
			}
			start := x
			for x < w && dark(bc.At(b.Min.X+x, b.Min.Y+y)) {
				x++
			}
			if twoD {
				fmt.Fprintf(&path, "M%d %dh%dv1h-%dz", start+quiet, y+quiet, x-start, x-start)
			} else {
				fmt.Fprintf(&path, "M%d %dh%dv%dh-%dz", start+quiet, quiet/2, x-start, barH, x-start)
			}
		}
	}
	vw, vh := w+2*quiet, h+2*quiet
	if !twoD {
		vh = h + quiet
	}
	var out bytes.Buffer
	fmt.Fprintf(&out, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" width="%d" height="%d" shape-rendering="crispEdges" preserveAspectRatio="xMidYMid meet">`, vw, vh, vw*4, vh*4)
	fmt.Fprintf(&out, `<rect width="%d" height="%d" fill="#fff"/><path fill="#000" d="%s"/></svg>`, vw, vh, path.String())
	return out.Bytes(), nil
}

func dark(c color.Color) bool {
	r, g, b, _ := c.RGBA()
	return r+g+b < 3*0x8000
}

// writeSVG sends a barcode; it never carries anything but rectangles.
func writeSVG(w http.ResponseWriter, svg []byte, cache bool) {
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if cache {
		w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "private, no-cache")
	}
	_, _ = w.Write(svg)
}

// ---- reading a photo ----

// scanned is a barcode found in a photo.
type scanned struct {
	Format string `json:"format"`
	Value  string `json:"value"`
}

var zxingFormat = map[gozxing.BarcodeFormat]string{
	gozxing.BarcodeFormat_QR_CODE: "qr", gozxing.BarcodeFormat_AZTEC: "aztec", gozxing.BarcodeFormat_DATA_MATRIX: "datamatrix",
	gozxing.BarcodeFormat_PDF_417: "pdf417", gozxing.BarcodeFormat_CODE_128: "code128", gozxing.BarcodeFormat_EAN_13: "ean13",
	gozxing.BarcodeFormat_EAN_8: "ean8", gozxing.BarcodeFormat_UPC_A: "upca", gozxing.BarcodeFormat_UPC_E: "upce",
	gozxing.BarcodeFormat_CODE_39: "code39", gozxing.BarcodeFormat_CODE_93: "code93", gozxing.BarcodeFormat_ITF: "itf",
	gozxing.BarcodeFormat_CODABAR: "codabar",
}

// readCode finds the first barcode in a photo (JPEG, PNG or GIF): the 2D
// codes first, then the line codes, each also on the photo turned a quarter.
func readCode(data []byte) (*scanned, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, errors.New("that is not a photo this app can read (JPEG or PNG)")
	}
	bmp, err := gozxing.NewBinaryBitmapFromImage(img)
	if err != nil {
		return nil, err
	}
	hints := map[gozxing.DecodeHintType]interface{}{gozxing.DecodeHintType_TRY_HARDER: true}
	readers := []gozxing.Reader{
		zqrcode.NewQRCodeReader(), zaztec.NewAztecReader(), zdatamatrix.NewDataMatrixReader(),
		oned.NewMultiFormatUPCEANReader(hints), oned.NewCode128Reader(), oned.NewCode39Reader(), oned.NewCode93Reader(),
		oned.NewITFReader(), oned.NewCodaBarReader(),
	}
	tries := []*gozxing.BinaryBitmap{bmp}
	if bmp.IsRotateSupported() {
		if r, err := bmp.RotateCounterClockwise(); err == nil {
			tries = append(tries, r)
		}
	}
	for _, b := range tries {
		for _, rd := range readers {
			res, err := rd.Decode(b, hints)
			if err != nil || res == nil || res.GetText() == "" {
				continue
			}
			f := zxingFormat[res.GetBarcodeFormat()]
			if f == "" {
				continue
			}
			return &scanned{Format: f, Value: res.GetText()}, nil
		}
	}
	return nil, nil
}

// guessFormat picks a barcode for a number typed by hand: EAN-13 for 13
// digits with a good check digit, EAN-8 likewise, else Code 128.
func guessFormat(number string) string {
	n := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || r == '-' {
			return -1
		}
		return r
	}, number)
	if digitsOnly(n) && (len(n) == 13 || len(n) == 8) && eanCheck(n) {
		if len(n) == 13 {
			return "ean13"
		}
		return "ean8"
	}
	if n == "" {
		return ""
	}
	return "code128"
}

func eanCheck(n string) bool {
	sum := 0
	for i := len(n) - 2; i >= 0; i-- {
		d := int(n[i] - '0')
		if (len(n)-2-i)%2 == 0 {
			d *= 3
		}
		sum += d
	}
	return (10-sum%10)%10 == int(n[len(n)-1]-'0')
}
