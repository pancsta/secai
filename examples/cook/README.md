# AI-gent Cook

This demo presents data collection, gen AI, offers, stories, workflows, dynamic short-term memory, planning with a DAG, story navigation, progress, and clockmoji.

## Package Contents

- aigent-cook
- config.kdl
- am-dbg
- arpc
- README.md
- LICENSE.md

## Configuration

Providing an AI model is mandatory, edit `config.kdl`, section `AI`. The first non-disabled model will be used.

- for **OpenAI**, change `openaikey` to a working one
- for **Gemini**, disable `// OpenAI`, and enable `// Gemini`, change `geminikey` to a working one
- for **DeepSeek**, disable `// OpenAI`, enable `// DeepSeek`, change `deepseekkey` to a working one
- for **LMstudio**, disable `// OpenAI`, enable `// LMStudio`, set the correct `Model`

## Start

1. Start `./aigent-cook`
2. Connect to TUI `ssh localhost -p 7855 -o UserKnownHostsFile=/dev/null -o StrictHostKeyChecking=no`

## Start With Debugger

1. In `config.kdl`, set `Debug.DBGAddr` to `1` (or a custom addr)
2. Start `./am-dbg`
3. Start `./aigent-cook`

## Start With Mock Scenario

1. In `config.kdl`, set `Debug.Mock` to `true`
2. Start `./aigent-cook`

## Start REPL

1. Start `./aigent-cook`
2. Start `./arpc --dir tmp`

## Credits

- [ai-gents.work](https://ai-gents.work)
- [asyncmachine.dev](https://asyncmachine.dev)
