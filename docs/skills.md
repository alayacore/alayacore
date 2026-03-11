# Skills System

AlayaCore supports the Agent Skills specification from [agentskills.io](https://agentskills.io). Skills are packages of instructions, scripts, and resources that agents can discover and use to perform specific tasks.

## Usage

```sh
# With skills directory
alayacore --type openai --base-url https://api.openai.com/v1 --api-key $OPENAI_API_KEY --model gpt-4o --skill ./skills "extract text from document.pdf"
```

## Skill Directory Structure

```
skills/
├── SKILL.md          # Required: instructions + metadata
├── scripts/          # Optional: executable code
├── references/      # Optional: documentation
└── assets/          # Optional: templates, resources
```

## SKILL.md Format

Skills use YAML frontmatter followed by Markdown content:

```yaml
---
name: pdf-processing
description: Use this skill whenever the user wants to do anything with PDF files. This includes reading or extracting text/tables from PDFs, combining or merging multiple PDFs into one, splitting PDFs apart, rotating pages, adding watermarks, creating new PDFs, filling PDF forms, encrypting/decrypting PDFs, extracting images, and OCR on scanned PDFs to make them searchable.
license: Apache-2.0
---

# PDF Processing Skill

Instructions for the agent...
```

## How Skills Work

1. **Discovery**: At startup, AlayaCore scans the skills directory and loads only skill names and descriptions
2. **Activation**: When a task matches a skill's description, the agent can activate it to load full instructions
3. **Execution**: The agent follows the instructions, optionally running bundled scripts

Skills metadata is injected into the system prompt using XML format:

```xml
<available_skills>
  <skill>
    <name>pdf-processing</name>
    <description>Extract text and tables from PDF files...</description>
    <location>/path/to/skills/pdf/SKILL.md</location>
  </skill>
</available_skills>
```

## Skill Specification

| Field | Description |
|-------|-------------|
| `name` | 1-64 characters, lowercase letters, numbers, and hyphens only |
| `description` | 1-1024 characters, describes what the skill does AND when to use it |
| `license` | Optional, license name or reference |
| `compatibility` | Optional, environment requirements |
| `allowed-tools` | Optional, space-delimited list of pre-approved tools |
