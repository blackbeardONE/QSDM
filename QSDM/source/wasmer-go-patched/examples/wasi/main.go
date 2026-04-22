package main

import (
	"fmt"
	"io/ioutil"

	wasmer "github.com/wasmerio/wasmer-go/wasmer"
)

func main() {
	wasmBytes, err := ioutil.ReadFile("module.wasm")
	check(err)

	store := wasmer.NewStore(wasmer.NewEngine())
	module, err := wasmer.NewModule(store, wasmBytes)
	check(err)

	wasiEnv, err := wasmer.NewWasiStateBuilder("wasi-program").
		// Choose according to your actual situation
		// Argument("--foo").
		// Environment("ABC", "DEF").
		// MapDirectory("./", ".").
		Finalize()
	check(err)
	importObject, err := wasiEnv.GenerateImportObject(store, module)
	check(err)

	instance, err := wasmer.NewInstance(module, importObject)
	check(err)

	start, err := instance.Exports.GetWasiStartFunction()
	check(err)
	start()

	HelloWorld, err := instance.Exports.GetFunction("HelloWorld")
	check(err)
	result, _ := HelloWorld()
	fmt.Println(result)
}

func check(e error) {
	if e != nil {
		panic(e)
	}
}
