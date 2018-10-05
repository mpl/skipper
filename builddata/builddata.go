// Package build$data has facilities for working with build data files.
package builddata

import (
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"strings"
)

// OpenFile opens a build log or build report file for reading. Files can be
// gzipped or plain text. Callers are responsible for closing the file.
func OpenFile(file string) (io.ReadCloser, error) {
	var (
		f   io.ReadCloser
		err error
	)
	f, err = os.Open(file)
	if err != nil {
		return nil, fmt.Errorf("Could not open log file %v: %v", file, err)
	}
	// I tried using gzip.NewReader + gzip.ErrHeader to find if the file is not a gzip, didn't quite work.
	// Using the extension is probably OK and predictable enough.
	if strings.HasSuffix(file, ".gz") {
		f, err = gzip.NewReader(f)
		if err != nil {
			return nil, fmt.Errorf("Could not gunzip file %v: %v", file, err)
		}
	}
	return f, nil
}
