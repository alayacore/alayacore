# Skills System

AlayaCore supports the [Agent Skills](https://agentskills.io) specification. Skills are packages of instructions, scripts, and resources that extend the agent's capabilities — the LLM discovers them at startup and activates them on demand.

## How It Works

1. **Discovery** — At startup, AlayaCore scans the skill directories and loads each skill's name and description from its `SKILL.md` frontmatter.
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

```sh
# Single skill directory
alayacore --skill ./skills/weather

# Multiple skill directories
alayacore --skill ./skills/weather --skill ./skills/pdf

# With custom model config
alayacore --model-config ./my-model.conf --skill ./skills
```

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
| `name` | Yes | Skill identifier. 1-64 characters, lowercase letters, numbers, and hyphens only. |
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
