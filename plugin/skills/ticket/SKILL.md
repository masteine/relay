---
name: ticket
description: Relay stage "ticket". Turns the discussion into the task statement that becomes the Issue body. Invoked headlessly by `relay stage ticket` as /relay:ticket <context-file>.
---

You are running the **ticket** stage of Relay. The owner wrote `/ticket`: the
discussion is over and the task must now be stated once, clearly, as the Issue
body. That body is the specification every later stage works from, and the
discussion stays above it as the first comments.

## Input

`$ARGUMENTS` is the path to the context file (format: docs/protocol.md §9).
Read it completely. The first owner comment is the original request; the
comments after it are the discussion, including your own `## Relay · discuss`
replies and the owner's answers.

## What to write

A `title` and a `body`, in the language of the discussion.

- `title`: imperative and specific, at most 80 characters. "Додати темну тему
  з перемикачем у налаштуваннях", not "Темна тема".
- `body`, Markdown with exactly these sections:
  - `## Задача` — what to build or fix, in a paragraph.
  - `## Контекст` — what was learned in the discussion that a stranger would
    need: decisions the owner made, constraints, where in the code it lives.
  - `## Критерії приймання` — a checklist (`- [ ]`) of observable outcomes.
    Each item is something a test or a human check can confirm.
  - `## Не в обсязі` — what was explicitly left out, or "—".

Where the owner said "you decide", write the decision you proposed in the
discussion as a decision, not as an open question.

## Rules

- Read the repository only to check a path or a name you mention. Do not
  write or edit files, do not run git.
- Do not invent requirements that were not discussed; a short body is better
  than a padded one.
- Do not include a `## Relay` heading or a footer; the wrapper writes the body
  into the Issue and posts its own comment.

## Output

Return the structured result: `title` and `body`.
