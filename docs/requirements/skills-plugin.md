# Skills Plugin System

## Purpose

Agent definitions are static markdown files with no runtime extensibility. There is no way to add reusable instruction snippets that span across roles. The skills system provides a file-based plugin mechanism for defining, discovering, and injecting reusable instruction sets into agent prompts.

## Requirements

- Skills must be defined as markdown files with YAML frontmatter (name, description, roles, tags)
- Skills must support three-tier resolution with shadowing: project-local > user-level > installed defaults
- Project-local skills must shadow user-level skills of the same name
- Skills must be filterable by agent role
- Empty roles list must mean the skill applies to all agents
- Skills must be searchable by keyword with weighted scoring (name > description/tags > body)
- Agents must be able to create new skills at runtime (written to project-local directory)
- Relevant skills must be automatically injected into agent prompts at launch time
- Skills must use kebab-case filenames
- The system must ship with default skills for common workflows (commit conventions, testing patterns, review checklists)

## Acceptance criteria

- `skill list` shows all discoverable skills across all resolution tiers
- `skill list --role <role>` filters to skills applicable to that role
- `skill load <name>` outputs a single skill's formatted content
- `skill search <query>` returns scored results matching the query
- `skill create <name> <desc> <body>` writes a new skill file to the project-local directory
- `skill prompt <role>` outputs assembled skills content for prompt injection
- A project-local skill with the same name as a user-level skill replaces it
- Skills with empty roles appear for all agents
- Agent launch process includes skill prompt output in the system prompt

## Status

Implemented
