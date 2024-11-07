cd instruments
for file in *.go; do
    wasm_file="${file%.go}.wasm"
    tinygo build -o "$wasm_file" -target wasi "$file"
done
