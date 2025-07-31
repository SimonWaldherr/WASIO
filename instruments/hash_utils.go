package main

import (
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"os"
	"strings"
)

type Payload struct {
	Params map[string]string `json:"params"`
}

func main() {
	var payload Payload
	if err := json.NewDecoder(os.Stdin).Decode(&payload); err != nil {
		fmt.Println("Error: invalid payload")
		return
	}

	input := payload.Params["input"]
	operation := strings.ToLower(payload.Params["op"])

	if input == "" {
		fmt.Println("Usage: /hash_utils?op=sha256&input=hello")
		fmt.Println("Operations: md5, sha1, sha256, sha512, base64encode, base64decode, hexencode, hexdecode")
		return
	}

	data := []byte(input)

	switch operation {
	case "md5":
		h := md5.New()
		h.Write(data)
		fmt.Printf("MD5: %x\n", h.Sum(nil))

	case "sha1":
		h := sha1.New()
		h.Write(data)
		fmt.Printf("SHA1: %x\n", h.Sum(nil))

	case "sha256":
		h := sha256.New()
		h.Write(data)
		fmt.Printf("SHA256: %x\n", h.Sum(nil))

	case "sha512":
		h := sha512.New()
		h.Write(data)
		fmt.Printf("SHA512: %x\n", h.Sum(nil))

	case "base64encode", "b64encode":
		encoded := base64.StdEncoding.EncodeToString(data)
		fmt.Printf("Base64 encoded: %s\n", encoded)

	case "base64decode", "b64decode":
		decoded, err := base64.StdEncoding.DecodeString(input)
		if err != nil {
			fmt.Printf("Error decoding base64: %v\n", err)
			return
		}
		fmt.Printf("Base64 decoded: %s\n", string(decoded))

	case "hexencode":
		encoded := hex.EncodeToString(data)
		fmt.Printf("Hex encoded: %s\n", encoded)

	case "hexdecode":
		decoded, err := hex.DecodeString(input)
		if err != nil {
			fmt.Printf("Error decoding hex: %v\n", err)
			return
		}
		fmt.Printf("Hex decoded: %s\n", string(decoded))

	case "all":
		// Generate all hashes
		fmt.Printf("Input: %s\n", input)
		fmt.Printf("Length: %d bytes\n\n", len(data))

		algorithms := []struct {
			name string
			hash hash.Hash
		}{
			{"MD5", md5.New()},
			{"SHA1", sha1.New()},
			{"SHA256", sha256.New()},
			{"SHA512", sha512.New()},
		}

		for _, alg := range algorithms {
			alg.hash.Write(data)
			fmt.Printf("%s: %x\n", alg.name, alg.hash.Sum(nil))
		}

		fmt.Printf("\nEncodings:\n")
		fmt.Printf("Base64: %s\n", base64.StdEncoding.EncodeToString(data))
		fmt.Printf("Hex: %s\n", hex.EncodeToString(data))

	default:
		fmt.Printf("Error: unsupported operation '%s'\n", operation)
		fmt.Println("Operations: md5, sha1, sha256, sha512, base64encode, base64decode, hexencode, hexdecode, all")
		return
	}
}
