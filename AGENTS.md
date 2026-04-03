- Write temporary files in `tmp/` and clean up files older than 7 days
- Go versions should match across `go.mod`, `mise.toml` and examples in `examples/`
- If you encounter any pre-existing issues. Add them to the plan and handle them last.
- Agents are not allowed to write into `AGENTS.md`
- If you encounter a changed file, that you didn't edit. Ask if you should revert it or commit it.
- Before presenting a plan, identify any ambigious parts of it. Ask questions until none remain.
- Always run tests with `-race` to test for race-conditions.
- All new 3rd party dependencies need to be approved. That applies to code, packages and workflows.

### References

- [README](README.md)
- [System Architecture](docs/architecture.md)
- [Original Design Document](docs/design.md)
- [Guide for Developers and Agents](docs/contributing.md)
- Various other docs in `docs/`
