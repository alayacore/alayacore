# Skills System

AlayaCore supports the [Agent Skills](https://agentskills.io) specification. Skills are packages of instructions, scripts, and resources that extend the agent's capabilities — the LLM discovers them at startup and activates them on demand.

## How It Works

1. **Discovery** — At startup, AlayaCore scans each `--skill` container one level deep and loads the name and description from every subdirectory's `SKILL.md` frontmatter.
2. **Injection** — Skill metadata is injected into the system prompt so the LLM knows what's available:
   ```xml
   <available_skills>
     <skill>
       <name>weather</name>
       <description>Use this skill whenever the user wants to get weather information...</description>
       <location>/path/to/skills/weather/SKILL.md</location>
     </skill>
   </available_skills>
   ```
3. **Activation** — When a task matches a skill's description, the LLM reads the `<location>` file using `read_file` to load the full instructions.
4. **Execution** — The agent follows the loaded instructions, optionally running bundled scripts via the `execute_command` tool.

## Usage

`--skill` takes a **container** directory, not a skill. AlayaCore loads every
immediate subdirectory of it that contains a `SKILL.md`, so one flag brings in all
of the skills in that folder:

```
skills/
├── weather/
│   └── SKILL.md      ← loaded
├── pdf/
│   └── SKILL.md      ← loaded
└── notes/
    └── SKILL.md      ← loaded
```

```sh
# all skills under ./skills — this is the normal case
alayacore --skill ./skills

# several containers (e.g. project skills plus personal ones); repeat the flag
alayacore --skill ./skills --skill ~/.alayacore/skills

# with a custom config directory
alayacore --config-path ./my-config --skill ./skills
```

> **Do not point `--skill` at a single skill's directory.** `--skill ./skills/weather`
> treats `weather/` as the container and looks for `weather/<something>/SKILL.md` —
> finding none, it loads nothing and reports no error. Measured: one flag per skill
> (`--skill ./skills/weather --skill ./skills/pdf`) loads **0** skills; `--skill ./skills`
> loads both. This is the most common way the feature appears broken when it is not.

## What Discovery Guarantees

Every one of these was measured against the loader; the silent ones are the reason
to keep the layout in mind.

| Situation | Result |
|---|---|
| `PATH/<dir>/SKILL.md` exists | loaded — all such subdirectories, from one flag |
| `PATH/<group>/<dir>/SKILL.md` (one extra level) | **not loaded, silently** — the scan is exactly one level deep, not recursive |
| `--skill PATH/<skill>` (the skill dir itself) | **nothing loaded, silently** — see the warning above |
| `PATH` does not exist | skipped, no error |
| subdirectory without `SKILL.md` | skipped, no error |
| a plain file inside `PATH` | skipped |
| frontmatter `name:` ≠ the directory name | that skill is **dropped**, and reported at startup as a load error |
| frontmatter `name:` outside the naming rules below | same: dropped and reported |
| same skill name from two containers | both kept (the prompt lists it twice), and reported as a duplicate at startup |
| no `--skill` at all | no skills; the system prompt omits the skills section entirely |

Paths are resolved against the working directory, so either give an absolute path
or run from the project root.

Because the name must equal its directory name, the naming rules apply to the
**folder** as well: 1–64 characters, lowercase letters, digits and hyphens only, no
leading, trailing or consecutive hyphens. `My_Skill/` can never load, and the
reported error is about the name.

Relative paths inside a `SKILL.md` are resolved from that skill's own directory —
the same directory `<location>` names in the injected XML.

## Skill Directory Structure

```
my-skill/
├── SKILL.md          # Required: instructions + metadata
├── scripts/          # Optional: executable scripts
├── references/       # Optional: reference documentation
└── assets/           # Optional: templates, resources
```

## SKILL.md Format

A skill's `SKILL.md` file uses YAML frontmatter followed by Markdown instructions:

```yaml
---
name: pdf-processing
description: Use this skill whenever the user wants to do anything with PDF files. This includes reading or extracting text/tables from PDFs, combining or merging multiple PDFs into one, splitting PDFs apart, rotating pages, adding watermarks, creating new PDFs, filling PDF forms, encrypting/decrypting PDFs, extracting images, and OCR on scanned PDFs to make them searchable.
license: Apache-2.0
---

# PDF Processing Skill

Instructions for the agent...

## Available Scripts

- `scripts/extract-text.sh <file>` — Extract text from a PDF
- `scripts/merge.sh <input1> <input2> <output>` — Merge two PDFs
```

### Frontmatter Fields

| Field | Required | Description |
|-------|----------|-------------|
| `name` | Yes | Skill identifier. 1-64 characters, lowercase letters, numbers, and hyphens only. Must match the directory name. |
| `description` | Yes | Describes what the skill does **and when to use it**. 1-1024 characters. This is what the LLM uses to decide whether to activate the skill. |
| `license` | No | License name or reference |
| `compatibility` | No | Environment requirements |
| `allowed-tools` | No | Space-delimited list of pre-approved tools |

### Writing Good Descriptions

The description serves as the trigger for skill activation. Be specific about **when** the skill should be used:

```yaml
# Good — clear trigger conditions
description: Use this skill whenever the user wants to get weather information. This includes current weather, forecasts, temperature, humidity, wind, and weather conditions for any city or region.

# Bad — too vague
description: Weather information.
```

## Example: Weather Skill

```
skills/weather/
├── SKILL.md
└── scripts/
    └── weather.sh
```

**SKILL.md:**

```yaml
---
name: weather
description: Use this skill whenever the user wants to get weather information. This includes current weather, forecasts, temperature, humidity, wind, and weather conditions for any city or region.
---

# Weather Skill

Get weather information using the weather script.

## Usage

```sh
./scripts/weather.sh "City name"
```

- **Note**: Use English or Pinyin for city names (e.g. Use "Wuhan" instead of "武汉")
```

When the user asks "what's the weather in Tokyo?", the LLM:
1. Matches the query against the skill description
2. Reads `<location>` (e.g. `/path/to/skills/weather/SKILL.md`) using `read_file`
3. Reads the full instructions from `SKILL.md`
4. Runs `scripts/weather.sh "Tokyo"` via the `execute_command` tool
5. Reports the results back to the user

## Skill Specification

For the full specification, see [agentskills.io](https://agentskills.io).
