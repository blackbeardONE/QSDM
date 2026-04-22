//go:build tinygo.wasm && js && wasm
// +build tinygo.wasm,js,wasm

package wallet

import (
	"errors"
	"github.com/blackbeardONE/QSDM/wasm_modules/wallet/walletcore"
	"github.com/blackbeardONE/QSDM/wasm_modules/wallet/walletcrypto"
	"syscall/js"
)

var keyPair *walletcrypto.KeyPair

var memory = js.Global().Get("wasmMemory")

func init() {
	var err error
	keyPair, err = walletcrypto.GenerateKeyPair()
	if err != nil {
		panic("Failed to generate key pair: " + err.Error())
	}
}

// readString reads a string from WASI memory given pointer and length
func readString(ptr uint32, length uint32) string {
	mem := memory.Get("buffer")
	uint8Array := js.Global().Get("Uint8Array").New(mem)
	bytes := make([]byte, length)
	for i := uint32(0); i < length; i++ {
		bytes[i] = byte(uint8Array.Call("get", ptr+i).Int())
	}
	return string(bytes)
}

// writeBytesToMemory writes bytes to WASI memory and returns pointer
func writeBytesToMemory(data []byte) (uint32, uint32, error) {
	mem := memory.Get("buffer")
	uint8Array := js.Global().Get("Uint8Array").New(mem)
	ptr := uint32(1024) // Example fixed pointer, should be dynamic allocation in real implementation
	for i, b := range data {
		uint8Array.SetIndex(ptr+uint32(i), js.ValueOf(b))
	}
	return ptr, uint32(len(data)), nil
}

// signTransaction signs transaction data using walletcrypto package
func signTransaction(data []byte) ([]byte, error) {
	if keyPair == nil {
		return nil, errors.New("key pair not initialized")
	}
	return keyPair.Sign(data)
}

// GetBalance returns the wallet balance
func GetBalance() int {
	return walletcore.GetBalance()
}

// SendTransaction processes a transaction.
// recipientPtr and recipientLen specify the recipient string in WASI memory.
// amount is the amount to send.
// Returns 1 on success, 0 on failure.
func SendTransaction(this js.Value, args []js.Value) interface{} {
	recipientPtr := uint32(args[0].Int())
	recipientLen := uint32(args[1].Int())
	amount := args[2].Int()
	recipient := readString(recipientPtr, recipientLen)
	// Use walletcore SendTransaction for core logic
	success := walletcore.SendTransaction(recipient, amount)
	if success {
		return 1
	}
	return 0
}

// SignTransaction signs arbitrary transaction data passed as pointer and length.
// Returns pointer to signature and length packed in uint64 (high 32 bits length, low 32 bits pointer).
func SignTransaction(this js.Value, args []js.Value) interface{} {
	dataPtr := uint32(args[0].Int())
	dataLen := uint32(args[1].Int())
	mem := memory.Get("buffer")
	uint8Array := js.Global().Get("Uint8Array").New(mem)
	data := make([]byte, dataLen)
	for i := uint32(0); i < dataLen; i++ {
		data[i] = byte(uint8Array.Call("get", dataPtr+i).Int())
	}
	signature, err := signTransaction(data)
	if err != nil {
		return uint64(0)
	}
	ptr, length, err := writeBytesToMemory(signature)
	if err != nil {
		return uint64(0)
	}
	// Pack length and pointer into uint64: high 32 bits length, low 32 bits pointer
	result := (uint64(length) << 32) | uint64(ptr)
	return result
}

func main() {
	// WASI modules do not have a main loop like JS WASM.
	// Initialization code can be added here if needed.
}
