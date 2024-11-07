package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
)

func main() {
	// Open and read the file
	fileContent, err := ioutil.ReadFile("/data/input.txt")
	if err != nil {
		fmt.Println("Error reading file:", err)
		return
	}

	// Process the file content (e.g., counting lines)
	lines := len(bytes.Split(fileContent, []byte("\n")))
	fmt.Printf("File has %d lines.\nContent: %s", lines, fileContent)
}
