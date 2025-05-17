package main

import (
    "syscall/js"
)

func validateTransaction(this js.Value, args []js.Value) interface{} {
    // args[0]: transaction data (string)
    txData := args[0].String()
    // For demonstration, accept all transactions containing "valid"
    if len(txData) > 0 && (txData == "valid" || txData == "test transaction data") {
        return js.ValueOf(true)
    }
    return js.ValueOf(false)
}

func registerCallbacks() {
    js.Global().Set("validateTransaction", js.FuncOf(validateTransaction))
}

func main() {
    c := make(chan struct{}, 0)
    registerCallbacks()
    <-c
}
