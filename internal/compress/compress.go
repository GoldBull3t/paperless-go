package compress

import (
	"encoding/base64"
	"strings"
	"sync"

	"github.com/klauspost/compress/zstd"
)

var (
	encoder     *zstd.Encoder
	decoder     *zstd.Decoder
	encoderOnce sync.Once
	decoderOnce sync.Once
)

const Prefix = "zstd:"

func getEncoder() *zstd.Encoder {
	encoderOnce.Do(func() {
		enc, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedDefault))
		if err == nil {
			encoder = enc
		}
	})
	return encoder
}

func getDecoder() *zstd.Decoder {
	decoderOnce.Do(func() {
		dec, err := zstd.NewReader(nil)
		if err == nil {
			decoder = dec
		}
	})
	return decoder
}

// CompressText compresses text with Zstandard if it's long enough to benefit,
// prefixing with "zstd:". If text is short or fails, returns plain text.
func CompressText(raw string) string {
	if len(raw) < 128 {
		return raw
	}
	enc := getEncoder()
	if enc == nil {
		return raw
	}

	compressed := enc.EncodeAll([]byte(raw), make([]byte, 0, len(raw)/2))
	if len(compressed) >= len(raw) {
		// No compression benefit
		return raw
	}

	return Prefix + base64.StdEncoding.EncodeToString(compressed)
}

// DecompressText decompresses text if it has the "zstd:" prefix.
// If it does not have the prefix, it returns the raw string untouched.
func DecompressText(val string) string {
	if !strings.HasPrefix(val, Prefix) {
		return val
	}

	b64 := strings.TrimPrefix(val, Prefix)
	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return val
	}

	dec := getDecoder()
	if dec == nil {
		return val
	}

	decompressed, err := dec.DecodeAll(data, nil)
	if err != nil {
		return val
	}

	return string(decompressed)
}

