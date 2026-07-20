# QSDM Project Contribution Guidelines

Thank you for your interest in contributing to the Quantum-Secure Dynamic Mesh Ledger (QSDM) project. To maintain code quality and consistency, please follow these guidelines:

## Code Style
- Follow Go and Rust idiomatic styles.
- Use `gofmt` for Go code formatting.
- Use `rustfmt` for Rust code formatting.

## Language and Terminology
- Use clear, familiar words and short sentences in interfaces, documentation,
  logs, release notes, and contributor messages.
- Define technical terms when they are necessary. Do not use jargon merely as
  a shorter way to say `official`, `production`, or `accepted`.
- Use `canonical` only for its precise technical meaning, such as a normalized
  byte representation that every implementation must produce identically.
- Keep protocol and API names exact even when the surrounding explanation uses
  plain language.
- Follow the full plain-language policy in the
  [QSDM Build and Release Guidelines](BUILD_AND_RELEASE_GUIDELINES.md#plain-language-and-terminology).

## Commit Messages
- Use clear, concise commit messages.
- Follow the format: `type(scope): subject`
- Example: `feat(networking): add libp2p support`

## Branching
- Use feature branches for new features or bug fixes.
- Keep the main branch stable.

## Testing
- Write unit tests for new features.
- Ensure all tests pass before submitting a pull request.

## Pull Requests
- Provide a clear description of changes.
- Reference related issues.
- Ensure code passes linting and tests.

## Reporting Issues
- Provide detailed steps to reproduce.
- Include logs and error messages if applicable.

## Communication
- Use the project’s communication channels for discussions.
- Be respectful and constructive.

Thank you for helping improve QSDM!
