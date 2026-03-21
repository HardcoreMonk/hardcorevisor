# Test Coverage

## Targets
- Rust: minimum 40% (vmcore has many unsafe/FFI functions that are hard to unit test)
- Go: minimum 50%

## Measurement
```bash
# Rust
just rust-coverage    # outputs to target/coverage/

# Go
just go-coverage      # outputs to controller/coverage.html
```

## Current (as of v0.1.0)
- Rust: measured via cargo tarpaulin
- Go: measured via go test -coverprofile
- Total tests: 101 (82 Rust + 19 Go)
