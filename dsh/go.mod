// dsh/ holds the DeepSeek Harness plugin workspace (TypeScript, pnpm). It has
// no Go code. Declaring a module boundary here keeps the whole workspace out of
// the root module's zip, so `go install github.com/mnemon-dev/mnemon@latest`
// downloads only the CLI.
module github.com/mnemon-dev/mnemon/dsh

go 1.24.6
