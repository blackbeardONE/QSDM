package main

import (
    "syscall/js"
)

func getBalance(this js.Value, args []js.Value) interface{} {
    // For demonstration, return a fixed balance
    return js.ValueOf(1000)
}

func sendTransaction(this js.Value, args []js.Value) interface{} {
    // args[0]: recipient address
    // args[1]: amount
    recipient := args[0].String()
    amount := args[1].Int()
    // For demonstration, just print and return success
    println("Sending", amount, "to", recipient)
    return js.ValueOf(true)
}

func registerCallbacks() {
    js.Global().Set("getBalance", js.FuncOf(getBalance))
    js.Global().Set("sendTransaction", js.FuncOf(sendTransaction))
}

func main() {
    c := make(chan struct{}, 0)
    registerCallbacks()
    <-c
}
