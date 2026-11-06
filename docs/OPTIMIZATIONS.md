# bf++ Optimization Roadmap

Ideas are ordered by payoff. Nothing here changes the language semantics; the optimizer must preserve the [backend parity contract](FIXES.md#backend-parity-contract).

| # | Idea | Impact | Effort |
| - | - | - | - |
| OPT-1 | Shared bytecode optimizer pass | high | medium |
| OPT-2 | Default `-O2` for the C backend | high | done |
| OPT-3 | Select the C representation with `-d` | none (debug UX) | done |
| OPT-4 | C runtime/codegen improvements | medium | medium |
| OPT-5 | Interpreter hot-path fixes | high | small |
| OPT-6 | Compact IR and resolved calls | high | medium |
| OPT-7 | Compile-time tape registry without 64 KiB values | medium | small |
| OPT-8 | Emitter offset tracking and local pointer | medium | medium |
| OPT-9 | Emit C that the C compiler can optimize | medium | small |
| OPT-10 | Parser/diagnostics plumbing | low | small |

Everything below assumes FIX-1 and FIX-2/FIX-3 are applied first; optimizing buggy semantics is wasted work.

---

## OPT-1: Shared bytecode optimizer pass

Run a single optimizer over `[]instruction` after parsing and before both backends. Both the interpreter and the C emitter benefit immediately.

Suggested pipeline:

```text
optimize(program):
    for each function body:
        repeat until no change:
            fuseRuns(body)
            removeNoOps(body)
            foldClearLoops(body)
            foldMulLoops(body)
            foldScanLoops(body)
        remapLoopTargets(body)
```

### 1a. Fuse arithmetic runs

Merge runs of the same class and cancel opposing operations. This is safe because byte arithmetic and pointer arithmetic are modulo 65,536.

```text
fuseRuns(code):
    out := []
    pending := none

    flush():
        if pending.op != none and pending.delta != 0:
            out.append(pending)

    for inst in code:
        if inst.op in {Add, Sub}:
            delta := inst.value or -inst.value
            if pending.op is arithmetic:
                pending.delta += delta
            else:
                flush()
                pending := {op: Add, delta: delta}
        else if inst.op in {MoveL, MoveR}:
            delta := inst.value or -inst.value
            if pending.op is movement:
                pending.delta += delta
            else:
                flush()
                pending := {op: MoveR, delta: delta}
        else:
            flush()
            out.append(inst)
    flush()
    return out
```

Example: `+5 -2 +7 >3 <1` becomes `+10 >2`.

There is one subtlety: `Add` with a negative delta can be emitted as `Sub`, and `MoveR` with a negative delta as `MoveL`; either way, the value must stay in a signed type during the pass and be converted back to unsigned at the end.

### 1b. Clear loops

Detect a loop whose body only changes the current cell and has a non-zero net delta. The byte wraps, so any non-zero delta eventually reaches zero.

```text
foldClearLoops(code):
    i := 0
    while i < len(code):
        if code[i].op == LoopStart:
            j := matchingEnd(i)
            body := code[i+1 : j]
            netCell, netMove, ok := analyzeBody(body)

            // ok == true only if body contains Add/Sub only,
            // netMove == 0, and netCell != 0
            if ok and netMove == 0 and netCell != 0:
                emit Set(0)
                i = j + 1
                continue
        i++
```

`[-]`, `[+]`, `[---]` all become `Set(0)`. This removes an entire loop per clear, which is common in bf++ code.

### 1c. Multiplication loops

Detect loops whose body only uses `+ - < >`, moves net zero, and changes the current cell by exactly `-1`. Each iteration adds a constant to a set of cells relative to the loop start.

```text
foldMulLoops(code):
    i := 0
    while i < len(code):
        if code[i].op == LoopStart:
            j := matchingEnd(i)
            terms := map<offset, coefficient>
            offset := 0
            valid := true

            for inst in code[i+1 : j]:
                switch inst.op:
                    Add k:  terms[offset] += k
                    Sub k:  terms[offset] -= k
                    MoveR k: offset += k
                    MoveL k: offset -= k
                    default: valid = false; break

            // A simple, always-terminating case: current cell decrements by 1.
            if valid and offset == 0 and terms[0] == -1:
                delete(terms, 0)
                emit MulAdd(terms)
                emit Set(0)
                i = j + 1
                continue
        i++
```

`[->++<]` becomes "add `2 * current` to `cell[p+1]`, then clear `cell[p]`". Interpreter:

```go
case MulAdd:
    src := tape.data[tape.position]
    for _, t := range terms {
        tape.data[(tape.position + uint16(t.offset))&0xFFFF] += byte(int(src) * t.coeff)
    }
    tape.data[tape.position] = 0
```

Generated C:

```c
{
    uint8_t _c = (*reg)[*ptr];
    (*reg)[(uint16_t)(*ptr + OFF)] += (uint8_t)(COEFF * _c);
    (*reg)[*ptr] = 0;
}
```

The strict `terms[0] == -1` condition keeps the transformation simple. Loops that subtract more than one from the current cell (`[--->+<]`) can be optimized later with division semantics, but that is a separate, riskier pass.

### 1d. Scan loops

A loop whose body only moves the pointer in one direction can be lowered to a dedicated op. It behaves exactly like the original, including the infinite-loop case.

```text
foldScanLoops(code):
    if body contains only MoveR and total delta > 0:
        emit ScanR(total)
    if body contains only MoveL and total delta < 0:
        emit ScanL(total)
```

Interpreter:

```go
case ScanR:
    for tape.data[tape.position] != 0 {
        tape.position += uint16(instr.value)
    }
```

C:

```c
while ((*reg)[*ptr]) { *ptr += N; } // uint16_t wraps
```

### 1e. Loop target remapping

Any pass that inserts or removes instructions invalidates the absolute `[`/`]` targets used by `instruction.value`. Keep this correct with a rebuild step:

```text
remapLoopTargets(old, new):
    // Walk both instruction lists in parallel, record old index -> new index.
    indexMap := map<oldIndex, newIndex>
    stack := []
    for newIndex, inst in new:
        if inst.op == LoopStart: stack.push(newIndex)
        if inst.op == LoopEnd:
            start := stack.pop()
            new[start].target = newIndex + 1
            new[newIndex].target = start
```

Passes that do not remove instructions are easier to implement in place. Passes that remove loops (1b, 1c, 1d) should build a new list and call the remap helper.

### 1f. Optional: constant cell propagation

For straight-line blocks, track known cell values and replace `+` runs on cells that are provably zero. This is rarely needed for hand-written bf++ and can be added later.

---

## OPT-2: Default `-O2` for the C backend

Accepted and already applied. `src/main.go` now defaults to `2`:

```go
var optimization = flag.Int("O", 2, "Optimization level 0 to 3")
```

This only affects the `-o` path. The generated C was previously unoptimized (the `-O` flag is forwarded directly to gcc). The README usage section should mention the default.

The interpreter is unaffected; its speed comes from OPT-5/OPT-6.

---

## OPT-3: Select the C representation with `-d`

Accepted and implemented. The default output uses `uint8_t *reg` / `reg[*ptr]`; `-d` switches to `uint8_t (*reg)[TAPE_SIZE]` / `(*reg)[*ptr]` for better gdb output.

### Emitter abstraction

Introduce a small emitter struct so the representation is chosen in one place:

```go
type cEmitter struct {
    sb    *strings.Builder
    debug bool // -d
}

func (e *cEmitter) regDecl(tape string) string {
    if e.debug {
        return fmt.Sprintf("uint8_t (*reg)[TAPE_SIZE] = &tape_%s;", tape)
    }
    return fmt.Sprintf("uint8_t *reg = tape_%s;", tape)
}

func (e *cEmitter) cell() string {
    if e.debug {
        return "(*reg)[*ptr]"
    }
    return "reg[*ptr]"
}

func (e *cEmitter) cellAddr() string {
    if e.debug {
        return "&(*reg)[*ptr]"
    }
    return "&reg[*ptr]"
}

func (e *cEmitter) switchTape(tape string) string {
    if e.debug {
        return fmt.Sprintf("reg = &tape_%s;", tape)
    }
    return fmt.Sprintf("reg = tape_%s;", tape)
}
```

Then every place in `write_c_body` that currently hardcodes `(*reg)[*ptr]` (arithmetic, I/O, push, pop) calls `e.cell()` / `e.cellAddr()`. The tape declarations always use `[TAPE_SIZE]` from FIX-2.

`-d` already prints bytecode; it now also switches the generated C to the debug representation. If the two concerns should be independent later, add `-g` and leave `-d` as "print bytecode".

Performance note: with `-O2` the two forms generate nearly identical code, so this is a debuggability switch, not a performance switch.

---

## OPT-4: C runtime and codegen improvements

### 4a. Avoid zeroing 64 KiB per call

Current code allocates and zeroes a full `BFStack` for every function call:

```c
BFStack *own_stack = (BFStack*)calloc(1, sizeof(BFStack));
```

Only `top` needs initialization. Two options, with different tradeoffs:

**Option A: plain stack allocation (simplest).**

```c
void f_x(BFStack *caller_stack) {
    BFStack own_stack;
    own_stack.top = 0;
    // ...
}
```

This removes heap traffic entirely but costs 64 KiB of C stack per active call. Deep recursion can overflow the process stack.

**Option B: pointer-stable slab/free-list (recommended if recursion should be supported).**

```c
typedef struct StackNode {
    BFStack stack;          // must be the first member
    struct StackNode *next;
} StackNode;

static StackNode *free_list = NULL;

static BFStack *stack_enter(void) {
    StackNode *n = free_list;
    if (n) {
        free_list = n->next;
    } else {
        n = malloc(sizeof *n);
        if (!n) { fprintf(stderr, "out of memory\n"); exit(1); }
    }
    n->stack.top = 0;
    return &n->stack;
}

static void stack_leave(BFStack *s) {
    StackNode *n = (StackNode *)s; // stack is the first member
    n->next = free_list;
    free_list = n;
}

void f_x(BFStack *caller_stack) {
    BFStack *own_stack = stack_enter();
    // ...
    stack_leave(own_stack);
}
```

Pointers stay stable across nested calls, so `caller_stack` remains valid, and stacks are reused instead of allocated per call. Do **not** use a flat `realloc`-growing pool unless every active frame's pointer is refreshed; growing would invalidate callers' `BFStack *`.

### 4b. Pop without copying 256 bytes

`bfstack_pop` returns `BFEntry` by value (256 bytes) today, and every `v` plus every transfer copies it. Use an out-parameter instead:

```c
static int bfstack_pop(BFStack *s, BFEntry *out);
```

Callers pass a local entry; transfer functions can use it directly, and `v` only copies `e.size` bytes to the tape. The return value can also carry an underflow flag; that form is now implemented in the generated C runtime. Underflow behavior itself is undefined and does not need to match the interpreter.

### 4c. Simplify macro emission

`write_macro_declarations` loops up to ten times to resolve macro ordering. C macro expansion is lazy: a `#define f_a(...)` may reference `f_b` before `f_b` is defined, as long as `f_b` is defined before any use of `f_a` in a function body. Since all macro definitions are emitted before all function bodies, one pass over `macros` is enough:

```text
for name in macros (any order):
    emit "#define f_" + name + "(stack) \\"
    emit body
    emit "\n//\n"
```

Keep the `callsSelf` conversion to real functions; recursive macros still cannot be expanded by the preprocessor.

### 4d. Portable terminal selection in generated C

`runtime.GOOS` selects termios vs Windows code for the **host** running `bf++`, not the target of the generated C. Emit both variants guarded by the C preprocessor instead:

```c
#ifdef _WIN32
#include <windows.h>
/* ... */
#else
#include <termios.h>
#include <unistd.h>
/* ... */
#endif
```

This is a backend-correctness cleanup rather than an optimization; it belongs in the same emitter change.

---

## OPT-5: Interpreter hot-path fixes

### 5a. Do not collect debug data unless `-id` is set

`src/interpreter.go` computes `depth` and scans `loop_stack` on every instruction, then discards the result unless `*interpreter_debug` is set:

```go
depth := len(loop_stack)
firstIter := true
for _, li := range loop_stack {
    if li.count > 0 { firstIter = false; break }
}

if *interpreter_debug { ... }
```

Move the computation inside the debug branch:

```go
if *interpreter_debug {
    depth := len(loop_stack)
    firstIter := true
    for _, li := range loop_stack {
        if li.count > 0 { firstIter = false; break }
    }
    // ... print debug info ...
}
```

This is the highest-impact, lowest-effort interpreter change.

### 5b. One stack pointer per frame

Already part of FIX-1. Once every frame stores its effective `*bfStack`, delete `currentStack()`. Stack operations become one pointer load instead of an upward scan.

### 5c. Resolve calls once

`case 9` and `case 10` call `FUNCTS[instr.f_name]`, which hashes a string on every call. Add a resolution pass before execution:

```text
resolveCalls(functions, externs):
    for each function f:
        for i, inst in f.instructs:
            if inst.op == Call:
                inst.funcIndex = indexOf(inst.f_name)
                inst.f_name = ""
```

The interpreter then uses `program.functions[instr.funcIndex]`. The same index can be used by the C emitter.

### 5d. Per-frame loop stack

The interpreter keeps one global `loop_stack` for all frames. Since loops are balanced within a function body, it works, but it costs work on every call and makes debugging state confusing. Store `loops []loopInfo` in `interpFrame` and index `top.loops`. The `[` op pushes to `top.loops`; `]` reads `top.loops`.

### 5e. Tape indexing

`uint16` arithmetic is correct but forces a conversion at every access. A `[]byte` with an `int` index and a mask is often faster in Go:

```go
const tapeMask = 0xFFFF

func cell(tape []byte, pos int) *byte {
    return &tape[pos&tapeMask]
}

// Add
tape[pos&tapeMask] += byte(delta)
// Move
pos = (pos + n) & tapeMask
```

Benchmark before adopting; the compiler may already minimize the conversion cost.

### 5f. Optional: inline non-recursive macros

Macros are inlined by definition in the language semantics, but the interpreter still pushes a frame per call. The parser could splice a copy of a non-recursive macro body at each call site, then run OPT-1 over the result. Recursive macros (detected by `callsSelf`) must keep the frame-based implementation. Tradeoff: faster calls, larger bytecode.

---

## OPT-6: Compact instruction representation

`instruction` in `src/helper.go` is much larger than it needs to be:

```go
type instruction struct {
    instr    uint8
    value    uint64
    f_name   string
    other    any
    switches map[byte]string
}
```

`other` is unused. `value` is always small except for jump targets and move/add counts. `f_name` and `switches` are only used by a few opcodes. For a 100k-instruction program, the current layout costs several megabytes and hurts cache locality in the interpreter.

Suggested layout:

```go
type instruction struct {
    op  uint8
    arg uint32 // add/sub/move value, loop target, function index
    aux uint32 // switch table index, none = 0xFFFFFFFF
}

type Program struct {
    code      []instruction
    functions []function
    switches  []switchTable
    names     []string
}

type switchTable struct {
    cases   [256]uint32 // function index per byte, 0xFFFFFFFF for none
    def     uint32
    hasDef  bool
}
```

A resolution pass converts string names to indices and moves switch maps into `switches`. The interpreter's dispatch switch then reads only compact values, and the C emitter resolves names in the same pass.

`switchTable` as a 256-entry array also removes the `map[byte]string` lookup from the `case 10` hot path. The memory cost is 1 KiB per switch instruction, which is fine; use a sorted slice if a program has hundreds of switches.

---

## OPT-7: Compile-time tape registry without 64 KiB values

`TAPES` is currently `map[string]tape_t`, and `tape_t` contains `data [0xFFFF + 1]byte`. Merely ranging over the map copies 64 KiB per tape, even though parsing only needs the name. The interpreter then builds a second array by copying each tape again.

Compile-time state should be names only:

```go
var tapeNames = map[string]struct{}{"main": {}}
```

Interpreter runtime owns the storage:

```go
type tapeRuntime struct {
    data []byte
    pos  uint16
}

func buildTapes(names map[string]struct{}) ([]tapeRuntime, map[string]int) {
    index := make(map[string]int, len(names))
    tapes := make([]tapeRuntime, 0, len(names))
    for name := range names {
        index[name] = len(tapes)
        tapes = append(tapes, tapeRuntime{data: make([]byte, 0x10000)})
    }
    return tapes, index
}
```

This removes both the compile-time 64 KiB map values and the startup copy pass. A program with ten named tapes currently copies 640 KiB just to read ten names.

---

## OPT-8: Emitter offset tracking and local pointer

After OPT-2, gcc will do much of this itself. It still helps at `-O0` and in the interpreter, and it improves generated source readability.

### 8a. Track a base offset per straight-line block

Reordering arithmetic within a basic block is safe because cell additions commute modulo 256. Flush at any instruction that observes or changes control flow: loops, calls, switches, tape switches, I/O, pushes, pops.

```text
pendingAdds := map<offset, delta>   // offset relative to block-entry pointer
baseDelta := 0

emitAdd(offset, delta): pendingAdds[offset] += delta
emitMove(n):            baseDelta += n

flush():
    // Apply adds while *ptr is still at the block-entry position,
    // then commit the net movement once.
    for off, delta in pendingAdds where delta != 0:
        emit cell(off) += delta
    if baseDelta != 0:
        emit "*ptr += baseDelta;"
    clear pendingAdds, baseDelta
```

This turns long runs like `>3 +1 >2 +1 <5` into a single pointer update plus two cell updates, instead of six memory operations.

### 8b. Cache `reg` and `*ptr` in locals per block

```c
{
    uint8_t (*_r)[TAPE_SIZE] = reg;
    uint16_t _p = *ptr;

    // ... use _r[(_p + off) & 0xFFFF] ...

    reg = _r;       // only if needed
    *ptr = _p;
}
```

The upper-register syntax may differ; the concept is to load the two globals once per basic block rather than once per instruction. Flush at calls because the callee may switch tapes or move the pointer.

This optimization is more invasive than OPT-8a and should be benchmarked against `-O2` output before adoption.

---

## OPT-9: Emit C that the C compiler can optimize

The IR passes in OPT-1 reduce the work the program does. This section is about the other half: making the emitted C easy for `cc -O2` to optimize further. The two compound. With the default now at `-O2`, small codegen choices make a measurable difference.

### 10a. Give generated functions internal linkage

The whole program is one translation unit. Emit `static void f_%s(...)` for generated functions and extern wrappers, including their forward declarations; keep `main` non-static. This lets GCC/Clang inline small functions, propagate constants across calls, and drop unused ones. It also avoids polluting the global namespace with `f_*` symbols.

Macros already expand via the preprocessor, so they are unaffected.

### 10b. Mark tiny runtime helpers `static inline`

`bfstack_push`, `bfstack_pop`, `tape_copy_out`, `tape_copy_in`, and `tape_zero` are on the hottest paths. Emit them as `static inline` so every `v`, `^`, and transfer does not become a function call, especially at `-O0`/`-O1`.

```c
static inline int bfstack_pop(BFStack *s, BFEntry *out) { ... }
static inline void tape_copy_out(uint8_t *tape, uint16_t pos, uint8_t *dst, uint8_t n) { ... }
```

### 10c. Add `restrict` where pointers cannot alias

The tape and scratch buffers never overlap:

```c
static inline void tape_copy_in(uint8_t *restrict tape, uint16_t pos,
                                const uint8_t *restrict src, uint8_t n) {
    for (uint8_t i = 0; i < n; i++) tape[(uint16_t)(pos + i)] = src[i];
}
```

This lets the compiler unroll/vectorize the copy loop and assume no aliasing. Do **not** put `restrict` on pointers that can legitimately alias by design.

### 10d. Keep hot state in locals

`reg` and `ptr` are globals. Every function call forces the compiler to assume they changed, so it must reload them across calls. OPT-8b's block-local `_r`/`_p` with a flush at call boundaries is the main win. Marking callees `static` (10a) also lets the compiler reason about them, but globals still need reloads at calls.

### 10e. Prefer wider local position values

A block-local `uint32_t p` with `(uint16_t)p` at access sites avoids repeated 16-bit loads/stores of `*ptr`. The `uint16_t` cast preserves wrapping semantics. Benchmark this against the current `uint16_t *ptr` form; on x86-64 it is often a small win, on 8/16-bit targets it may not be.

### 10f. Avoid undefined behavior in generated C

The optimizer is allowed to assume no out-of-bounds access, no signed overflow, and no null dereference. The 65,536-byte tape and wrap-safe copy helpers from FIX-2 remove the main UB source. Without them, a higher optimization level can legitimately change behavior for boundary-crossing programs.

### 10g. Emit canonical loops and constants

Give the C compiler recognizable patterns:

- `[-]` clears a cell: emit `cell = 0`, not a loop.
- Multiplication loops: emit straight-line adds, then clear.
- Scan loops: emit `while (cell) { *ptr += N; }`.
- Keep add/move amounts as visible constants; OPT-1 reduces their volume first.

These are the same transformations as OPT-1, but emitted as simple C they are also easier for `cc` to analyze, inline, and fold.

### 10h. One translation unit, no LTO needed

Everything is emitted into one `.c` file, so `static` functions and `static inline` helpers are visible to the optimizer. `-flto` would add nothing here.

### 10i. Do not disable strict aliasing

Do not add `-fno-strict-aliasing`. Structure the pointers so aliasing is explicit instead; disabling strict aliasing usually costs more than it saves.

Measurement: compile the same program with the old and new emitter at `-O2`, compare runtime, and inspect `gcc -S` output. Verify parity with the harness in [FIXES.md](FIXES.md#appendix-b-parity-test-harness).

---

## OPT-10: Parser and diagnostics plumbing

Low impact, but simple:

- Parse numeric literals from a source slice instead of `temp += string(code[counter])`, which is quadratic:

  ```go
  start := counter
  for counter < n && isDigit(code[counter]) { counter++ }
  v, err := strconv.ParseUint(code[start:counter], 10, 64)
  ```

- Precompute line-start offsets once per source in `compCtx` instead of walking from byte zero in `offsetToPos` for every diagnostic:

  ```go
  type compCtx struct {
      globalSrc  string
      lineStarts []int
      baseOffset uint64
      funcName   string
  }
  ```

- Replace package-level mutable state (`FUNCTS`, `TAPES`, `EXTERN`, `IMPORTED`, backend globals) with a compiler/backend struct. This does not affect generated code speed but makes tests independent and prevents state leaking between compilations.

---

## Benchmark plan

Add small fixtures and a repeatable measurement procedure so changes can be evaluated:

| Fixture | Stress |
| - | - |
| `bench_loop.bfpp` | tight `+`/`-` loop |
| `bench_mul.bfpp` | `[->+++<]` multiplication loop |
| `bench_scan.bfpp` | `[>]` scanning |
| `bench_call.bfpp` | many function calls with args/returns |
| `bench_tapes.bfpp` | frequent `:(name)` switching |

Procedure:

1. Run each fixture with `-i` and record wall time.
2. Compile with `-o` and run natively, using the same `-O` level for before/after comparisons.
3. Verify output equality between backends on every run (Appendix B in [FIXES.md](FIXES.md#appendix-b-parity-test-harness)).
4. For Go-internal functions (optimizer passes, `offsetToPos`), add standard `go test -bench` benchmarks in `src/`.

Track interpreter throughput (instructions/second) and native runtime separately; OPT-1 and OPT-5/OPT-6 affect the interpreter, OPT-2/OPT-4/OPT-8 affect native code.
