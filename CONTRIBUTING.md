# Contributing to UWAS

Thank you for your interest in contributing to UWAS.

## Getting Started

1. Fork the repository
2. Clone your fork: `git clone https://github.com/YOUR_USERNAME/uwas.git`
3. Create a branch: `git checkout -b my-feature`
4. Make your changes
5. Run tests: `make test`
6. Run lints: `make lint`
7. Commit and push
8. Open a Pull Request

## Development

### Requirements

- Go 1.23 or later
- Make (optional, for convenience)

### Build

```bash
make dev     # Development binary
make build   # Production binary (stripped, versioned)
```

### Test

```bash
make test    # go test ./...
make lint    # go vet + staticcheck
```

### Project Structure

- `cmd/uwas/` — CLI entry point
- `internal/` — Private packages (core server logic)
- `pkg/` — Public reusable packages (fastcgi protocol, htaccess parser)
- `docs/` — Design documents
- `test/` — Integration and benchmark tests

## Guidelines

### Code

- Follow standard Go conventions (`gofmt`, `go vet`)
- Use `log/slog` via `internal/logger` for all logging
- Prefer stdlib over external dependencies
- Write tests for new functionality

### Commits

- One logical change per commit
- Use clear, descriptive commit messages
- Reference issue numbers when applicable

### Pull Requests

- One feature or fix per PR
- Include tests
- Update documentation if behavior changes
- Keep PRs small and focused

### Issues

- Open an issue before starting significant work
- Use issue templates when available
- Include reproduction steps for bug reports

## Code of Conduct

Be respectful and constructive. We're here to build great software together.

## License

By contributing, you agree that your contributions will be licensed under the [Apache License 2.0](LICENSE).
