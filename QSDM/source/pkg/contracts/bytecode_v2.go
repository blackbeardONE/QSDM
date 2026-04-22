package contracts

// V2 WASM contract bytecode with linear memory for state management.
// These modules use WASM memory to store balances, vote counts, and escrow state,
// providing richer on-chain logic than the v1 arithmetic-only modules.

// tokenV2WASM exports:
//   - memory (1 page = 64KB)
//   - set_balance(addr_slot i32, amount i32)   stores amount at memory[addr_slot*4]
//   - get_balance(addr_slot i32) -> i32         reads balance from memory[addr_slot*4]
//   - transfer(from_slot i32, to_slot i32, amount i32) -> i32  returns 1 on success, 0 on insufficient
//   - add(i32,i32)->i32  (v1 compat)
//   - sub(i32,i32)->i32  (v1 compat)
var tokenV2WASM []byte

// votingV2WASM exports:
//   - memory (1 page)
//   - vote_yes(proposal_slot i32) -> i32    increments yes count, returns new total
//   - vote_no(proposal_slot i32) -> i32     increments no count, returns new total
//   - get_yes(proposal_slot i32) -> i32     returns yes votes
//   - get_no(proposal_slot i32) -> i32      returns no votes
//   - increment(i32)->i32  (v1 compat)
var votingV2WASM []byte

// escrowV2WASM exports:
//   - memory (1 page)
//   - deposit(slot i32, amount i32) -> i32     stores amount, returns slot
//   - get_amount(slot i32) -> i32              reads deposited amount
//   - get_status(slot i32) -> i32              0=empty, 1=held, 2=released, 3=refunded
//   - release(slot i32) -> i32                 sets status=released, returns 1 or 0
//   - refund(slot i32) -> i32                  sets status=refunded, returns 1 or 0
//   - clamp(i32,i32,i32)->i32  (v1 compat)
var escrowV2WASM []byte

func init() {
	tokenV2WASM = buildTokenV2()
	votingV2WASM = buildVotingV2()
	escrowV2WASM = buildEscrowV2()
}

// buildTokenV2 constructs a WASM module with memory-backed token balances.
// WAT equivalent:
//
//	(module
//	  (memory (export "memory") 1)
//	  (func (export "set_balance") (param $slot i32) (param $amt i32)
//	    (i32.store (i32.mul (local.get $slot) (i32.const 4)) (local.get $amt)))
//	  (func (export "get_balance") (param $slot i32) (result i32)
//	    (i32.load (i32.mul (local.get $slot) (i32.const 4))))
//	  (func (export "transfer") (param $from i32) (param $to i32) (param $amt i32) (result i32)
//	    (local $fb i32) (local $tb i32)
//	    (local.set $fb (call $get_balance (local.get $from)))
//	    (if (i32.lt_u (local.get $fb) (local.get $amt)) (then (return (i32.const 0))))
//	    (call $set_balance (local.get $from) (i32.sub (local.get $fb) (local.get $amt)))
//	    (local.set $tb (call $get_balance (local.get $to)))
//	    (call $set_balance (local.get $to) (i32.add (local.get $tb) (local.get $amt)))
//	    (i32.const 1))
//	  (func (export "add") (param i32) (param i32) (result i32) (i32.add (local.get 0) (local.get 1)))
//	  (func (export "sub") (param i32) (param i32) (result i32) (i32.sub (local.get 0) (local.get 1)))
//	)
func buildTokenV2() []byte {
	w := newWasmBuilder()

	// Types
	t_ii := w.addFuncType([]byte{0x7f, 0x7f}, nil)          // (i32,i32)->()
	t_i_i := w.addFuncType([]byte{0x7f}, []byte{0x7f})       // (i32)->(i32)
	t_iii_i := w.addFuncType([]byte{0x7f, 0x7f, 0x7f}, []byte{0x7f}) // (i32,i32,i32)->(i32)
	t_ii_i := w.addFuncType([]byte{0x7f, 0x7f}, []byte{0x7f}) // (i32,i32)->(i32)

	// Functions: set_balance(0), get_balance(1), transfer(2), add(3), sub(4)
	w.addFunctions([]int{t_ii, t_i_i, t_iii_i, t_ii_i, t_ii_i})

	// Memory: 1 page
	w.addMemory(1)

	// Exports
	w.addExport("memory", 0x02, 0)
	w.addExport("set_balance", 0x00, 0)
	w.addExport("get_balance", 0x00, 1)
	w.addExport("transfer", 0x00, 2)
	w.addExport("add", 0x00, 3)
	w.addExport("sub", 0x00, 4)

	// Code bodies
	// func 0: set_balance(slot, amt): i32.store(slot*4, amt)
	w.addCodeBody(0, []byte{
		0x20, 0x00, // local.get 0 (slot)
		0x41, 0x04, // i32.const 4
		0x6c,       // i32.mul
		0x20, 0x01, // local.get 1 (amt)
		0x36, 0x02, 0x00, // i32.store align=2 offset=0
		0x0b, // end
	})

	// func 1: get_balance(slot) -> i32: i32.load(slot*4)
	w.addCodeBody(0, []byte{
		0x20, 0x00, // local.get 0 (slot)
		0x41, 0x04, // i32.const 4
		0x6c,       // i32.mul
		0x28, 0x02, 0x00, // i32.load align=2 offset=0
		0x0b, // end
	})

	// func 2: transfer(from, to, amt) -> i32
	// 2 locals: fb(i32), tb(i32)
	w.addCodeBody(2, []byte{
		// local.set $fb = call get_balance(from)
		0x20, 0x00, // local.get 0 (from)
		0x10, 0x01, // call func 1 (get_balance)
		0x21, 0x03, // local.set 3 (fb)
		// if fb < amt: return 0
		0x20, 0x03, // local.get 3 (fb)
		0x20, 0x02, // local.get 2 (amt)
		0x49,       // i32.lt_u
		0x04, 0x40, // if (no result)
		0x41, 0x00, // i32.const 0
		0x0f,       // return
		0x0b,       // end if
		// set_balance(from, fb - amt)
		0x20, 0x00, // local.get 0 (from)
		0x20, 0x03, // local.get 3 (fb)
		0x20, 0x02, // local.get 2 (amt)
		0x6b,       // i32.sub
		0x10, 0x00, // call func 0 (set_balance)
		// local.set $tb = call get_balance(to)
		0x20, 0x01, // local.get 1 (to)
		0x10, 0x01, // call func 1 (get_balance)
		0x21, 0x04, // local.set 4 (tb)
		// set_balance(to, tb + amt)
		0x20, 0x01, // local.get 1 (to)
		0x20, 0x04, // local.get 4 (tb)
		0x20, 0x02, // local.get 2 (amt)
		0x6a,       // i32.add
		0x10, 0x00, // call func 0 (set_balance)
		// return 1
		0x41, 0x01, // i32.const 1
		0x0b,       // end
	})

	// func 3: add(a,b) -> i32
	w.addCodeBody(0, []byte{
		0x20, 0x00, 0x20, 0x01, 0x6a, 0x0b,
	})

	// func 4: sub(a,b) -> i32
	w.addCodeBody(0, []byte{
		0x20, 0x00, 0x20, 0x01, 0x6b, 0x0b,
	})

	return w.build()
}

// buildVotingV2 constructs a WASM module with memory-backed vote tallies.
// Memory layout per proposal slot: [slot*8] = yes_count, [slot*8+4] = no_count
func buildVotingV2() []byte {
	w := newWasmBuilder()

	t_i_i := w.addFuncType([]byte{0x7f}, []byte{0x7f}) // (i32)->(i32)

	// Functions: vote_yes(0), vote_no(1), get_yes(2), get_no(3), increment(4)
	w.addFunctions([]int{t_i_i, t_i_i, t_i_i, t_i_i, t_i_i})

	w.addMemory(1)

	w.addExport("memory", 0x02, 0)
	w.addExport("vote_yes", 0x00, 0)
	w.addExport("vote_no", 0x00, 1)
	w.addExport("get_yes", 0x00, 2)
	w.addExport("get_no", 0x00, 3)
	w.addExport("increment", 0x00, 4)

	// func 0: vote_yes(slot) -> i32: mem[slot*8] += 1; return new count
	// Uses local 1 (i32) to hold the new value.
	w.addCodeBody(1, []byte{
		// compute new value: mem[slot*8] + 1 -> local 1
		0x20, 0x00, 0x41, 0x08, 0x6c, // slot*8 (addr for load)
		0x28, 0x02, 0x00,             // i32.load
		0x41, 0x01,                   // i32.const 1
		0x6a,                         // i32.add
		0x21, 0x01,                   // local.set 1
		// store: mem[slot*8] = local 1
		0x20, 0x00, 0x41, 0x08, 0x6c, // slot*8 (addr for store)
		0x20, 0x01,                   // local.get 1
		0x36, 0x02, 0x00,             // i32.store
		// return new value
		0x20, 0x01, // local.get 1
		0x0b,
	})

	// func 1: vote_no(slot) -> i32: mem[slot*8+4] += 1; return new count
	w.addCodeBody(1, []byte{
		// compute new value
		0x20, 0x00, 0x41, 0x08, 0x6c, 0x41, 0x04, 0x6a, // slot*8+4
		0x28, 0x02, 0x00,
		0x41, 0x01, 0x6a,
		0x21, 0x01,
		// store
		0x20, 0x00, 0x41, 0x08, 0x6c, 0x41, 0x04, 0x6a,
		0x20, 0x01,
		0x36, 0x02, 0x00,
		// return
		0x20, 0x01,
		0x0b,
	})

	// func 2: get_yes(slot) -> i32: return mem[slot*8]
	w.addCodeBody(0, []byte{
		0x20, 0x00, 0x41, 0x08, 0x6c,
		0x28, 0x02, 0x00,
		0x0b,
	})

	// func 3: get_no(slot) -> i32: return mem[slot*8+4]
	w.addCodeBody(0, []byte{
		0x20, 0x00, 0x41, 0x08, 0x6c, 0x41, 0x04, 0x6a,
		0x28, 0x02, 0x00,
		0x0b,
	})

	// func 4: increment(x) -> x+1
	w.addCodeBody(0, []byte{
		0x20, 0x00, 0x41, 0x01, 0x6a, 0x0b,
	})

	return w.build()
}

// buildEscrowV2 constructs a WASM module with memory-backed escrow state.
// Memory layout per slot: [slot*8] = amount, [slot*8+4] = status (0=empty, 1=held, 2=released, 3=refunded)
func buildEscrowV2() []byte {
	w := newWasmBuilder()

	t_ii_i := w.addFuncType([]byte{0x7f, 0x7f}, []byte{0x7f})       // (i32,i32)->(i32)
	t_i_i := w.addFuncType([]byte{0x7f}, []byte{0x7f})               // (i32)->(i32)
	t_iii_i := w.addFuncType([]byte{0x7f, 0x7f, 0x7f}, []byte{0x7f}) // (i32,i32,i32)->(i32)

	// Functions: deposit(0), get_amount(1), get_status(2), release(3), refund(4), clamp(5)
	w.addFunctions([]int{t_ii_i, t_i_i, t_i_i, t_i_i, t_i_i, t_iii_i})

	w.addMemory(1)

	w.addExport("memory", 0x02, 0)
	w.addExport("deposit", 0x00, 0)
	w.addExport("get_amount", 0x00, 1)
	w.addExport("get_status", 0x00, 2)
	w.addExport("release", 0x00, 3)
	w.addExport("refund", 0x00, 4)
	w.addExport("clamp", 0x00, 5)

	// func 0: deposit(slot, amount) -> slot
	// mem[slot*8] = amount; mem[slot*8+4] = 1 (held); return slot
	w.addCodeBody(0, []byte{
		// store amount
		0x20, 0x00, 0x41, 0x08, 0x6c, // slot*8
		0x20, 0x01,                   // amount
		0x36, 0x02, 0x00,             // i32.store
		// store status = 1
		0x20, 0x00, 0x41, 0x08, 0x6c, 0x41, 0x04, 0x6a, // slot*8+4
		0x41, 0x01,       // i32.const 1
		0x36, 0x02, 0x00, // i32.store
		// return slot
		0x20, 0x00,
		0x0b,
	})

	// func 1: get_amount(slot) -> i32
	w.addCodeBody(0, []byte{
		0x20, 0x00, 0x41, 0x08, 0x6c,
		0x28, 0x02, 0x00,
		0x0b,
	})

	// func 2: get_status(slot) -> i32
	w.addCodeBody(0, []byte{
		0x20, 0x00, 0x41, 0x08, 0x6c, 0x41, 0x04, 0x6a,
		0x28, 0x02, 0x00,
		0x0b,
	})

	// func 3: release(slot) -> i32: if status==1 set status=2, return 1; else return 0
	w.addCodeBody(0, []byte{
		// check status == 1
		0x20, 0x00, 0x41, 0x08, 0x6c, 0x41, 0x04, 0x6a, // addr
		0x28, 0x02, 0x00, // i32.load
		0x41, 0x01,       // i32.const 1
		0x46,             // i32.eq
		0x04, 0x7f,       // if (result i32)
		// set status = 2
		0x20, 0x00, 0x41, 0x08, 0x6c, 0x41, 0x04, 0x6a,
		0x41, 0x02,
		0x36, 0x02, 0x00,
		0x41, 0x01, // return 1
		0x05,       // else
		0x41, 0x00, // return 0
		0x0b,       // end if
		0x0b,       // end func
	})

	// func 4: refund(slot) -> i32: if status==1 set status=3, return 1; else return 0
	w.addCodeBody(0, []byte{
		0x20, 0x00, 0x41, 0x08, 0x6c, 0x41, 0x04, 0x6a,
		0x28, 0x02, 0x00,
		0x41, 0x01,
		0x46,
		0x04, 0x7f,
		0x20, 0x00, 0x41, 0x08, 0x6c, 0x41, 0x04, 0x6a,
		0x41, 0x03,
		0x36, 0x02, 0x00,
		0x41, 0x01,
		0x05,
		0x41, 0x00,
		0x0b,
		0x0b,
	})

	// func 5: clamp(val, min, max) -> i32 (same as v1)
	w.addCodeBody(0, []byte{
		0x20, 0x01, 0x20, 0x02, 0x20, 0x00, 0x20, 0x00, 0x20, 0x02,
		0x4a, 0x1b, 0x20, 0x00, 0x20, 0x01, 0x48, 0x1b, 0x0b,
	})

	return w.build()
}

// wasmBuilder is a helper for constructing valid WASM modules programmatically.
type wasmBuilder struct {
	types   [][]byte
	funcs   []int
	memory  int
	exports []wasmExport
	codes   []wasmCode
}

type wasmExport struct {
	name string
	kind byte
	idx  int
}

type wasmCode struct {
	numLocalsI32 int
	body         []byte
}

func newWasmBuilder() *wasmBuilder {
	return &wasmBuilder{memory: -1}
}

func (w *wasmBuilder) addFuncType(params, results []byte) int {
	var enc []byte
	enc = append(enc, 0x60)
	enc = append(enc, byte(len(params)))
	enc = append(enc, params...)
	enc = append(enc, byte(len(results)))
	enc = append(enc, results...)
	w.types = append(w.types, enc)
	return len(w.types) - 1
}

func (w *wasmBuilder) addFunctions(typeIndices []int) {
	w.funcs = typeIndices
}

func (w *wasmBuilder) addMemory(pages int) {
	w.memory = pages
}

func (w *wasmBuilder) addExport(name string, kind byte, idx int) {
	w.exports = append(w.exports, wasmExport{name, kind, idx})
}

func (w *wasmBuilder) addCodeBody(numLocalsI32 int, body []byte) {
	w.codes = append(w.codes, wasmCode{numLocalsI32, body})
}

func (w *wasmBuilder) build() []byte {
	var mod []byte
	mod = append(mod, 0x00, 0x61, 0x73, 0x6d) // magic
	mod = append(mod, 0x01, 0x00, 0x00, 0x00) // version

	// Type section (id=1)
	mod = appendSection(mod, 1, func() []byte {
		var s []byte
		s = appendULEB128(s, len(w.types))
		for _, t := range w.types {
			s = append(s, t...)
		}
		return s
	})

	// Function section (id=3)
	mod = appendSection(mod, 3, func() []byte {
		var s []byte
		s = appendULEB128(s, len(w.funcs))
		for _, idx := range w.funcs {
			s = appendULEB128(s, idx)
		}
		return s
	})

	// Memory section (id=5)
	if w.memory >= 0 {
		mod = appendSection(mod, 5, func() []byte {
			var s []byte
			s = appendULEB128(s, 1) // 1 memory
			s = append(s, 0x00)     // limits: no max
			s = appendULEB128(s, w.memory)
			return s
		})
	}

	// Export section (id=7)
	mod = appendSection(mod, 7, func() []byte {
		var s []byte
		s = appendULEB128(s, len(w.exports))
		for _, e := range w.exports {
			s = appendULEB128(s, len(e.name))
			s = append(s, []byte(e.name)...)
			s = append(s, e.kind)
			s = appendULEB128(s, e.idx)
		}
		return s
	})

	// Code section (id=10)
	mod = appendSection(mod, 10, func() []byte {
		var s []byte
		s = appendULEB128(s, len(w.codes))
		for _, c := range w.codes {
			var body []byte
			if c.numLocalsI32 > 0 {
				body = appendULEB128(body, 1) // 1 local declaration
				body = appendULEB128(body, c.numLocalsI32)
				body = append(body, 0x7f) // i32
			} else {
				body = appendULEB128(body, 0) // 0 local declarations
			}
			body = append(body, c.body...)
			s = appendULEB128(s, len(body))
			s = append(s, body...)
		}
		return s
	})

	return mod
}

func appendSection(mod []byte, id int, contentFn func() []byte) []byte {
	content := contentFn()
	mod = append(mod, byte(id))
	mod = appendULEB128(mod, len(content))
	mod = append(mod, content...)
	return mod
}

func appendULEB128(buf []byte, val int) []byte {
	for {
		b := byte(val & 0x7f)
		val >>= 7
		if val != 0 {
			b |= 0x80
		}
		buf = append(buf, b)
		if val == 0 {
			break
		}
	}
	return buf
}
