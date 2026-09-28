package store

import (
	"bytes"
	"compress/zlib"
	_ "embed" // the compression dictionary
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/adler32"
	"io"
	"sync"
)

// ffprobe output is large (about 16 KB for a film with many subtitle tracks
// and chapters) and most of it is the same keys in every file. The store
// removes its white space and compresses it with zlib and a preset
// dictionary of typical ffprobe output, which makes it about fifteen times
// smaller, and stores it as a BLOB. Rows written before compression hold
// JSON text and read back as they are.
//
// A compressed value is a zlib stream whose header names its dictionary
// (the Adler-32 of the dictionary). JSON text never starts with the zlib
// header byte 0x78.

// probeDictV1 is the first compression dictionary: compacted ffprobe output
// of the synthetic test fixtures. It must never change, or stored probes
// could not be read; a better dictionary is added as a new version next to
// it (see dictionaries). TestProbeDictionaryIsUnchanged guards it.
//
//go:embed probedict_v1.txt
var probeDictV1 []byte

// dictionaries finds a dictionary by the ID in a zlib header.
var dictionaries = map[uint32][]byte{adler32.Checksum(probeDictV1): probeDictV1}

// currentDict is the dictionary new values are compressed with.
var currentDict = probeDictV1

// compressMin is the size below which JSON is stored as text: compressing a
// few bytes saves nothing.
const compressMin = 256

var zlibWriters = sync.Pool{New: func() any {
	w, _ := zlib.NewWriterLevelDict(io.Discard, zlib.DefaultCompression, currentDict)
	return w
}}

// packJSON returns the value to store for a JSON document: the text itself
// when it is short, or a compressed BLOB. White space outside strings is
// removed first; the JSON means the same.
func packJSON(s string) (any, error) {
	if len(s) < compressMin {
		return s, nil
	}
	src := []byte(s)
	var compact bytes.Buffer
	compact.Grow(len(src))
	if json.Compact(&compact, src) == nil {
		src = compact.Bytes()
	}
	var buf bytes.Buffer
	buf.Grow(len(src) / 8)
	w, _ := zlibWriters.Get().(*zlib.Writer)
	defer zlibWriters.Put(w)
	w.Reset(&buf)
	if _, err := w.Write(src); err != nil {
		return nil, fmt.Errorf("compressing JSON: %w", err)
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("compressing JSON: %w", err)
	}
	return buf.Bytes(), nil
}

// isPacked reports whether b starts with a zlib header (deflate with a 32 KB
// window) that names a preset dictionary.
func isPacked(b []byte) bool {
	return len(b) >= 6 && b[0] == 0x78 && b[1]&0x20 != 0 && binary.BigEndian.Uint16(b)%31 == 0
}

// unpackJSON reverses packJSON. Text (a row written before compression, or
// a short document) is returned as it is.
func unpackJSON(b []byte) (string, error) {
	if !isPacked(b) {
		return string(b), nil
	}
	id := binary.BigEndian.Uint32(b[2:6])
	dict, ok := dictionaries[id]
	if !ok {
		return "", fmt.Errorf("reading compressed JSON: unknown dictionary %08x", id)
	}
	r, err := zlibReader(bytes.NewReader(b), dict)
	if err != nil {
		return "", fmt.Errorf("reading compressed JSON: %w", err)
	}
	defer zlibReaders.Put(r)
	var out bytes.Buffer
	out.Grow(len(b) * 12)
	if _, err := io.Copy(&out, r); err != nil { // #nosec G110 -- the store wrote this data itself
		return "", fmt.Errorf("reading compressed JSON: %w", err)
	}
	return out.String(), nil
}

// zlibReaders reuses readers: reading every probe of a large library would
// otherwise allocate a decompression window per row.
var zlibReaders sync.Pool

func zlibReader(src io.Reader, dict []byte) (io.ReadCloser, error) {
	if r, ok := zlibReaders.Get().(io.ReadCloser); ok {
		if z, ok := r.(zlib.Resetter); ok {
			if err := z.Reset(src, dict); err != nil {
				return nil, err
			}
			return r, nil
		}
	}
	return zlib.NewReaderDict(src, dict)
}
