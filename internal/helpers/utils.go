package helpers

import (
	"os"
)

func MakeDir(dirpath string) error {
	if _, err := os.Stat(dirpath); os.IsNotExist(err) {
		return os.MkdirAll(dirpath, os.ModePerm)
	}

	return nil
}
