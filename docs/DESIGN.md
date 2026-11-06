# Design Notes and Non-Goals

## Correctness policy

> Make valid programs work correctly. Everything else is undefined behavior.

The practical consequence for this repo:

- A **valid** bf++ program must behave identically when run with `-i` and when compiled with `-o`.
- Anything the language does not define may behave differently between backends. That includes stack overflow/underflow, reading at EOF, wrong argument sizes, malformed source, invalid signatures, overflowing literals, and resource exhaustion.
- Diagnostics are best-effort. They are not a stability guarantee.

The concrete parity contract between backends is listed at the top of [FIXES.md](FIXES.md).

## Names

Names are deliberately permissive.

- `!(){}` is valid. It emits `f_` and works.
- UTF-8 names are allowed. GCC and clang accept UTF-8 identifiers, so generated C for `!(héllo)` is expected to compile with the supported compilers.
- Do not add a validation pass that restricts names to ASCII C identifiers.

One small doc/implementation mismatch exists and should be resolved by choosing one direction, not by adding a C-identifier validator:

- README lists `/` as a reserved character, but `keywordBytes` in `src/helper.go` does not reject it. Either add `/` to `keywordBytes` for a clean diagnostic or remove it from the README's reserved list. Since invalid names are UB, this is documentation-level only.

## Undefined behavior vs. crashes

Invalid input may fail however it fails. Parser panics on truncated source are therefore **not correctness bugs** under this policy. They are listed separately as optional robustness work in [FIXES.md](FIXES.md#appendix-a-optional-robustness) because they are cheap to avoid and make the tool nicer to use.

## C backend representation

The generated C uses `uint8_t (*reg)[TAPE_SIZE]` and `(*reg)[*ptr]` because it produces better debugger output in gdb. The simpler/faster-to-read form is `uint8_t *reg` with `reg[*ptr]`.

Decision: keep both forms and select between them with the `-d` flag.

- Default: `uint8_t *reg` / `reg[*ptr]`.
- `-d`: `uint8_t (*reg)[TAPE_SIZE]` / `(*reg)[*ptr]`, which is easier to inspect in a debugger.

The performance difference is negligible once the C compiler runs with optimization enabled, so this switch is about debugger UX, not speed. See [OPTIMIZATIONS.md](OPTIMIZATIONS.md#opt-3-select-the-c-representation-with--d).

## Optimization defaults

The default C compiler optimization level is `-O2` (previously `-O0`). Users can still override it with `-O0`..`-O3`.

This only affects the `-o` path; the bytecode interpreter is unaffected.

## External functions

External C functions are a C-backend feature only. Calling one with `-i` is invalid usage; a clear diagnostic is preferable to a silent no-op. See [FIXES.md](FIXES.md#a5-external-functions-under--i-are-silent-no-ops).

## C-side errors

Compile-time errors in generated C are the C compiler's responsibility. `bf++` does not duplicate checks that `cc` already performs — missing `main`, invalid external types, compiler-specific constructs, and similar C-side problems are reported by the C compiler. `bf++` only needs to propagate a non-zero exit status when the C compiler fails.

Runtime errors are handled by the generated code itself. Stack overflow, stack underflow during argument/return transfer, and argument size mismatches are checked in the emitted C runtime, which prints an error and exits non-zero. Matching the interpreter's behavior for these situations is not required, because they are undefined behavior.

This keeps the bf++ compiler small and avoids maintaining a second, weaker C validator.

## CI

The existing GitHub workflow is out of scope for this work. Correctness is enforced with local tests and the parity harness described in [FIXES.md](FIXES.md#appendix-b-parity-test-harness).
