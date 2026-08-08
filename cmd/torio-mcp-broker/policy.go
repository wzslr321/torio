package main

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/wzslr321/torio/internal/lima"
)

func loadPolicyDir(dir string) (lima.Set, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return lima.Set{}, errors.New("read MCP policy directory")
	}
	if len(entries) > lima.MaxPolicyServices {
		return lima.Set{}, fmt.Errorf("MCP policy directory exceeds %d services", lima.MaxPolicyServices)
	}
	documents := make(map[string][]byte, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return lima.Set{}, errors.New("inspect MCP policy document")
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return lima.Set{}, errors.New("every MCP policy document must be a regular file")
		}
		file, err := os.Open(dir + string(os.PathSeparator) + entry.Name())
		if err != nil {
			return lima.Set{}, errors.New("open MCP policy document")
		}
		data, readErr := io.ReadAll(io.LimitReader(file, lima.MaxPolicyDocumentBytes+1))
		closeErr := file.Close()
		if readErr != nil || closeErr != nil {
			return lima.Set{}, errors.New("read MCP policy document")
		}
		if len(data) > lima.MaxPolicyDocumentBytes {
			return lima.Set{}, errors.New("MCP policy document exceeds size limit")
		}
		documents[entry.Name()] = data
	}
	set, err := lima.ParseDocuments(documents)
	if err != nil {
		return lima.Set{}, fmt.Errorf("parse MCP policy: %w", err)
	}
	return set, nil
}
