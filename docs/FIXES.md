# bf++ Correctness Fixes

This document lists the fixes required for **valid** bf++ programs to behave correctly in both backends. Behavior outside the language definition is undefined and is not required to match; optional robustness work is separated at the end. The project policy is:

> Make valid programs work correctly. Everything else is undefined behavior.

See [DESIGN.md](DESIGN.md) for accepted decisions and non-goals.

## Status

FIX-1 through FIX-5 are implemented. Appendix A is optional robustness; A6's generated-C runtime check is implemented, the remaining appendix entries are not.

## Backend parity contract

Both `-i` (interpreter) and `-o` (C backend) must agree on all of the following for valid programs.

| Property | Required behavior |
| - | - |
| Tape size | 65,536 bytes, indices `0..65535` |
| Pointer arithmetic | wraps modulo 65,536 |
| Named tapes | independent data and pointer per tape |
| Value stack | 256 entries, each entry 1..255 bytes; using all 256 must work |
| Return order | first declared return ends up deepest, last on top |
| External functions | C backend only |

Anything not listed — stack overflow/underflow, reading at EOF, wrong argument sizes, malformed input — is undefined behavior. The backends may behave differently in those cases, including whether they diagnose them.

The fixes below assume this contract.

---

## FIX-1: Interpreter loses return values from `?(...)` calls (critical)

### Symptom

`examples/t_stack.bfpp` output differs between backends:

| Test | Interpreter | C backend |
| - | - | - |
| 3, return `99` | `0` | `99` |
| 4, `double(13)` | `0` | `26` |
| 5, `swap(9, 5)` | `0, 0` | `9, 5` |
| 11, 2-byte return | `0, 66` | `65, 66` |

### Root cause

Two independent defects in `src/interpreter.go`:

1. The `case 9` handler (`?` call, around lines 206-245) pushes the callee frame without setting `funcName`. The return-transfer check after each instruction (around lines 331-362) only runs when `finishing.funcName != ""`, so normal calls never transfer returns. Only `case 10` (switch) sets `funcName`, which is why returns there appear to work.
2. Return transfer is performed after an instruction, never when a frame reaches the end of its bytecode at the top of the loop (around lines 113-120). A frame whose last instruction is a call, or whose body is empty, is popped without transfer.

### Fix

Move return handling into the frame-pop path and let every frame carry its effective stack pointer and return slots. This also removes the `currentStack()` scan.

```go
type interpFrame struct {
    bytecode []instruction
    counter  uint64
    stack    *bfStack   // effective stack; macros snapshot the caller's stack
    retSlots []sigSlot  // nil or empty => no returns
    funcName string
}

// used by both case 9 and case 10
func pushCall(stack *[]interpFrame, fn function) {
    caller := (*stack)[len(*stack)-1].stack // every frame has a non-nil stack

    if fn.isMacro {
        *stack = append(*stack, interpFrame{
            bytecode: fn.instructs,
            stack:    caller, // macros share the caller's stack
        })
        return
    }

    callee := &bfStack{}
    if !transferArgs(caller, callee, fn.args, fn.name) {
        os.Exit(1)
    }
    *stack = append(*stack, interpFrame{
        bytecode: fn.instructs,
        stack:    callee,
        retSlots: fn.returns,
        funcName: fn.name,
    })
}

// main dispatch loop
for len(stack) > 0 {
    top := &stack[len(stack)-1]

    if top.counter >= uint64(len(top.bytecode)) {
        if len(top.retSlots) > 0 && len(stack) > 1 {
            caller := stack[len(stack)-2].stack
            if !transferReturns(top.stack, caller, top.retSlots, top.funcName) {
                os.Exit(1)
            }
        }
        stack = stack[:len(stack)-1]
        continue
    }

    // ... execute top.bytecode[top.counter] ...
}
```

Notes:

- The root frame gets `stack: rootStack`; all later frames inherit or create one.
- Delete the `currentStack` closure and the post-instruction return block.
- `parseSigSlots` returns a nil slice for both "no return signature" and `()`, so `len(retSlots) > 0` is the correct test either way.
- Calling an empty function with returns now transfers (and underflows) consistently with the C backend.

### Regression tests

Add fixtures covered by Appendix B:

- single return from `?` call;
- multiple returns and ordering;
- return from a function called via `switch`;
- function whose last instruction is a call, with returns.

---

## FIX-2: C backend tape is one byte too small (critical)

### Symptom

`src/compiler.go` emits `uint8_t tape_%s[0xffff]` (65,535 bytes), while the interpreter and README use 65,536. Index 65,535 is out of bounds, and multi-byte `^N` / `v` operations crossing the end read or write past the array.

Example: `!(main){ >65535 +65 . }` prints `A` with the interpreter. The generated C writes one byte past `tape_main`, which happens to work but is undefined behavior and can corrupt `ptr_main`.

### Fix

Use a 65,536-byte array and index single-cell operations directly. Multi-byte operations must wrap per byte.

```c
#define TAPE_SIZE 0x10000

uint8_t tape_main[TAPE_SIZE] = {0};
uint16_t ptr_main = 0;

uint8_t (*reg)[TAPE_SIZE] = &tape_main;
uint16_t *ptr = &ptr_main;

static inline uint8_t tape_load(uint8_t (*r)[TAPE_SIZE], uint16_t pos, uint16_t off) {
    return (*r)[(uint16_t)(pos + off)];
}

static inline void tape_store(uint8_t (*r)[TAPE_SIZE], uint16_t pos, uint16_t off, uint8_t v) {
    (*r)[(uint16_t)(pos + off)] = v;
}
```

Use the helpers (or a loop with per-byte wrap) for push/pop and cursor movement. Single-cell `+ - . ,` need no helper because `uint16_t` indices are always within a 65,536-byte array.

```c
// push copy
for (uint8_t i = 0; i < n; i++) {
    entry.data[i] = tape_load(reg, *ptr, i);
}

// pop
for (uint8_t i = 0; i < e.size; i++) {
    tape_store(reg, *ptr, i, e.data[i]);
}
```

Keep `src/helper.go`'s `tape_t` at `[0xFFFF + 1]byte`; it is already correct.

---

## FIX-3: C stack holds 255 entries instead of 256 (high)

### Symptom

`src/compiler.go` declares `uint8_t top;` and rejects pushes with `if (s->top == 255)`. The 256th entry is rejected, and if the check were relaxed without changing the type, `top++` would wrap from 255 to 0 and corrupt the stack.

Verified: a program pushing 256 entries prints `O` under the interpreter and fails with `runtime error: stack overflow` in the C backend.

### Fix

```c
typedef struct {
    BFEntry entries[256];
    uint16_t top;          // 0..256
} BFStack;

static void bfstack_push(BFStack *s, uint8_t sz, const uint8_t *bytes) {
    if (s->top >= 256) {
        fprintf(stderr, "runtime error: stack overflow\n");
        exit(1);
    }
    s->entries[s->top].size = sz;
    memcpy(s->entries[s->top].data, bytes, sz);
    s->top++;
}
```

The interpreter already allows 256 entries (`STACK_MAX_ENTRIES = 256`), so only the C side changes. Pushing more than 256 entries is undefined behavior and may differ between backends.

---

## FIX-4: Errors inside imported files point at the wrong source (medium)

### Symptom

When compiling `@(utils)`, `comp_extern` builds the child context with the caller's source and offset:

```go
childCtx := compCtx{
    globalSrc:  ctx.globalSrc, // root file
    baseOffset: ctx.baseOffset, // root offset
    funcName:   name + ".bfpp",
}
```

Errors in `utils.bfpp` are therefore rendered against the importing file's text, producing wrong line numbers and a wrong source snippet.

### Fix

The imported file is its own source document:

```go
childCtx := compCtx{
    globalSrc:  src, // the imported file's contents
    baseOffset: 0,
    funcName:   name + ".bfpp",
}
```

Function contexts created inside `comp_func` already add their body offset to the parent's `baseOffset`, so this composes correctly for functions in imports.

---

## FIX-5: C compiler failures are not propagated (low)

### Symptom

`compile_to_c` prints `compilation failed` when `invoke_compiler` returns an error, but returns normally, so `bf++` exits 0 even though no output binary was produced. It also discards the `os.WriteFile` error when writing the generated `.c` file.

Missing `main` and invalid external types are intentionally left to the C compiler (see [DESIGN.md](DESIGN.md#c-side-errors)). What bf++ must do is report that the C compiler failed and exit non-zero.

### Fix

```go
if err := os.WriteFile(out_name+".c", []byte(sb.String()), 0644); err != nil {
    fmt.Fprintln(os.Stderr, "error: could not write C source:", err)
    os.Exit(1)
}

if err := invoke_compiler(*output); err != nil {
    fmt.Fprintln(os.Stderr, "error: compilation failed:", err)
    os.Exit(1)
}
```

`compile_to_c` already prints the compiler command and connects stdout/stderr, so cc's diagnostics remain visible.

---

## Appendix A: optional robustness

These are not required by the correctness policy. They are cheap and improve the experience on invalid input.

### A1. Parser panics on truncated input

- `printf '@'` panics at `src/parser.go:697` because the error message reads `code[counter]` after `counter >= len(code)`.
- `printf '!(f)(n'` panics at `src/parser.go:155` because the signature parser indexes past the end after consuming `n`.

Fix by checking bounds before every read used in an error path and returning a normal `showError`:

```go
if counter >= uint64(len(code)) {
    return counter, []instruction{}, showError(ctx, code, counter, "error",
        "expected '(' after '@'")
}
```

The surrounding parsers already have the right error format; only the missing guards are needed.

### A2. Numeric literals overflow silently

`strconv.ParseUint` errors are discarded for `+`/`-`/`>`/`<` literals (`comp_add_sub_move`) and switch case values (`comp_case`), so an overflow becomes `0`. Push widths and signature sizes already check the error. `comp_add_sub_move` currently has no error return, so adding a diagnostic there requires changing its signature. For the helpers that already return an error:

```go
v, err := strconv.ParseUint(code[start:counter], 10, 64)
if err != nil {
    return ..., showError(ctx, code, start, "error",
        fmt.Sprintf("number literal out of range: %s", code[start:counter]))
}
```

Parse from a slice rather than building the string with `+=`; the current construction is quadratic in the literal length.

### A3. Silent reinterpretation of unknown suffixes

- `@4(foo)` falls through to import type `0` and imports `foo.bfpp`.
- `.4` is a debug-only print and `.5` falls back to ASCII.
- `^0` is treated as `^1`.

Reject unknown suffixes for clearer behavior. This is invalid input under the project policy, so it is optional.

### A4. Reading at EOF may differ (undefined behavior)

The language does not define what `,` stores at end of input, so the backends are not required to match.

- Interpreter: `pollKey` returns `0` when the read returns no data.
- C backend: `getchar()` returns `EOF` (-1), which becomes `255` when assigned to a `uint8_t` cell.

No fix is required. If consistency is wanted anyway, pick one value and use it on both sides; for example, make C store `0`:

```c
int _c = getchar();
(*reg)[*ptr] = (_c == EOF) ? 0 : (uint8_t)_c;
```

### A5. External functions under `-i` are silent no-ops

The verification loop accepts a name that exists in `EXTERN`. At runtime, `case 9` looks up `FUNCTS`, receives the zero value, and pushes an empty frame, so the call silently does nothing.

Since external functions are documented as C-backend-only, this is invalid usage and no behavior is guaranteed. A clear diagnostic is still nicer than a no-op:

```go
_, isFn := FUNCTS[cs.name]
_, isExt := EXTERN[cs.name]

if !isFn {
    if isExt {
        if *interpret {
            showError(cs.ctx, cs.code, cs.nameOffset, "error",
                fmt.Sprintf("external function '%s' requires compiling with -o", cs.name))
            not_found = true
        }
    } else {
        // existing "never defined" diagnostic
    }
}
```

### A6. C does not detect stack underflow during transfers (undefined behavior)

Underflow is outside the language definition, so the backends do not have to agree. The generated C runtime now detects underflow during argument/return transfer (`bfstack_pop` returns a flag) and writes a zero byte for `v`. The interpreter behaves the same way, but this remains an implementation choice, not a parity requirement.

The out-parameter form described in [OPTIMIZATIONS.md](OPTIMIZATIONS.md#4b-pop-without-copying-256-bytes) is what the generated C uses; it also avoids the previous 256-byte return copies.

---

## Appendix B: parity test harness

The single most valuable test is: run a fixture with the interpreter and with the C backend, then compare stdout. This would have caught FIX-1 immediately.

```go
// src/parity_test.go (package main)
func runInterpreter(t *testing.T, src string) string {
    t.Helper()
    cmd := exec.Command(bfBinary, "-i", src)
    cmd.Stdin = strings.NewReader("")
    out, err := cmd.Output()
    if err != nil {
        t.Fatalf("interpreter failed on %s: %v", src, err)
    }
    return string(out)
}

func runCBackend(t *testing.T, src string) string {
    t.Helper()
    dir := t.TempDir()
    exe := filepath.Join(dir, "prog")
    build := exec.Command(bfBinary, "-o", exe, src)
    if out, err := build.CombinedOutput(); err != nil {
        t.Fatalf("C backend failed on %s: %v\n%s", src, err, out)
    }
    cmd := exec.Command(exe)
    cmd.Stdin = strings.NewReader("")
    out, err := cmd.Output()
    if err != nil {
        t.Fatalf("native program failed on %s: %v", src, err)
    }
    return string(out)
}

func TestBackendParity(t *testing.T) {
    fixtures := []string{
        "testdata/return_values.bfpp",
        "testdata/switch_return.bfpp",
        "testdata/return_last_call.bfpp",
        "testdata/stack_256.bfpp",
        "testdata/tape_edge.bfpp",
    }
    for _, f := range fixtures {
        t.Run(filepath.Base(f), func(t *testing.T) {
            want := runInterpreter(t, f)
            got := runCBackend(t, f)
            if want != got {
                t.Fatalf("backend mismatch\ninterpreter: %q\nC backend:   %q", want, got)
            }
        })
    }
}
```

Fixture expectations:

| Fixture | Covers | Expected |
| - | - | - |
| `return_values.bfpp` | `?` call with 1 and 2 returns | matching values in order |
| `switch_return.bfpp` | function invoked via `$(...)` returns | matching values |
| `return_last_call.bfpp` | last instruction of a returning function is itself a call | matching values |
| `stack_256.bfpp` | 256 pushes | both backends accept |
| `tape_edge.bfpp` | `>65535` plus multi-byte `^N`/`v` across the edge | matching bytes |

Do not add undefined-behavior fixtures (EOF reads, overflow/underflow) to this harness; parity is not required there.

The interpreter spawns `stty`; fixtures in this harness should not depend on interactive input. If a fixture needs to read actual input, add a flag to skip terminal setup or run only the C backend for that case.
