---
name: inspect-image
description: Use this skill whenever a question can only be answered by looking at an image — checking a screenshot or a rendered page for a layout or rendering bug, reading a chart, diagram or photo, or confirming what a picture shows. It runs a separate AlayaCore instance that reads the image and answers in text, so the image itself never enters this conversation.
---

# Inspect Image Skill

Answer a question about one or more images **without the pixels entering this
conversation**.

## Why it exists

Reading an image with `read_file` puts it in the conversation history, and that
history is re-sent on every request. A loop that looks at an image each round —
render, look, fix, render — accumulates every image it ever looked at, in the
context and in the session file.

This skill sidesteps that. The script starts a **second AlayaCore** in
`--terseio` mode and hands it your prompt. That instance reads the image itself
and prints only its final answer. Your history records a command and a line of
text.

## Usage

```sh
./scripts/inspect-image.sh "<prompt naming the image path(s)>"
```

Run it with the `execute_command` tool and **`workdir` set to this skill's
directory** — the folder holding this file. That is the convention the system
prompt names for a skill's own scripts, and it is what makes the relative script
path above resolve.

Four rules:

- **Write the image path(s) into the prompt.** The prompt is the only argument
  the script takes, so how many images you name is yours to decide — one, or
  several to compare. The script never sees a path as such.
- **Pass the whole prompt as one quoted argument.** Paths with spaces, and
  quotes inside the prompt, depend on this.
- **Use absolute image paths.** The nested instance inherits this skill's
  directory as its working directory, so a relative path would be read against
  *here* rather than against your project.
- **Do not begin the prompt with `:`.** `--terseio` reads an input that starts
  with a colon as a command, not a prompt, so the run would fail as an unknown
  command — an error that says nothing about the image.

Ask for a **conclusion**, not a description. "Does the footer overlap the logo,
and where?" comes back shorter and more useful than "describe this image", and
it is the difference between one round and three.

## Output

The answer text on stdout, nothing else. Errors and the nested run's own
diagnostics go to stderr: exit `1` when the nested run failed or produced no
answer text, exit `2` when the script itself was called wrongly (no prompt, an
empty one, or one beginning with `:`).

## Configuration

`INSPECT_IMAGE_CONFIG` names the config directory the nested instance runs with
— an **absolute** path, same layout as `~/.alayacore` (`model.conf`,
`runtime.conf`, `themes/`). It is passed as `--config-path`, and **only when the
variable is set**. An unset variable must mean "use the caller's own
configuration" rather than "point the nested run at an empty directory": a
config directory with no `model.conf` is not left empty — AlayaCore writes a
default Ollama model into it, and the nested run then talks to `127.0.0.1`.

Setting it is worth doing for two reasons beyond choosing a model:

- **It is the only way to keep the nested instance off your MCP servers.** The
  MCP config is derived from the config directory and always loaded, and MCP
  tools are added *on top of* `--builtin-tools`, whatever that set contains. So
  on a shared config directory the nested agent receives every MCP tool you
  have, and the run waits for MCP initialization before it answers. A directory
  with a `model.conf` and **no `mcp.conf`** has neither problem.
- It lets the nested run use a different model, which is what you need when
  your active model cannot see images at all.

Keep that directory out of anything you share or commit: `model.conf` holds an
API key.

## Cost

Each call is a separate LLM run: seconds of latency and its own tokens. It is
worth it when the alternative is carrying an image through the rest of the
session, and not worth it for a question a text tool can answer.
