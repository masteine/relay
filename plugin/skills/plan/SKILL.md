---
name: plan
description: Relay stage "plan". Produces the implementation plan through a planner → architect → critic loop, writes it to docs/specs/, and posts an ADR summary. Invoked headlessly by `relay stage plan` as /relay:plan <context-file>.
---

You are running the **plan** stage of Relay. The task is stated in the Issue
body; the triage comment says how big it is and where it lives. Your job is to
produce a plan good enough that the implement stage can follow it without
asking questions, and to get that plan through an independent review before
anyone acts on it.

## Input

`$ARGUMENTS` is the path to the context file (format: docs/protocol.md §9).
Read it completely. Note the `Spec path:` line: the plan file goes exactly
there. Note the `Branch:` line: the wrapper commits the file to that branch
after you finish, so you do not run git.

## The loop

Run these as subagents, in this order, never in parallel:

1. **`relay:planner`** writes the draft. Give it: the Issue title and body, the
   triage comment, the key files, and the spec path. It returns the full plan
   in Markdown.
2. **`relay:architect`** reviews the draft for architectural soundness. Give it
   the draft and the task. It returns the strongest counterargument to the
   chosen option, at least one trade-off tension, and a synthesis path.
3. **`relay:critic`** evaluates the draft together with the architect's review
   against the quality bar below. It returns `APPROVE` or `REVISE` with a
   numbered list of required changes.
4. If `REVISE`: send the draft, the architect's review and the critic's list
   back to **`relay:planner`** for a revision, then go to step 2 again.
5. Stop after `APPROVE`, or after the third critic round. Three rounds without
   approval means the verdict is `NO_CONSENSUS`: publish the latest draft
   anyway and say so.

Each subagent gets everything it needs in its prompt. They do not share
memory, and they cannot read the context file unless you give them its path.

## Quality bar

The critic rejects, and you should too, a plan that misses any of these:

- Principles (3–5) and decision drivers (top 3) that the chosen option
  actually follows.
- At least two viable options with honest pros and cons, or one option with
  an explicit reason why the alternatives are invalid.
- Implementation steps that name files and functions; at least 80 % of the
  claims about the code cite a path.
- Acceptance criteria that a test or a human check can confirm; no "fast",
  "clean" or "robust" without a number or an observable.
- Every risk has a mitigation.
- Verification steps: which commands to run, what must be green.

## The spec file

Write the final plan to the spec path with these sections, in this order:

```
# <Issue title>

Issue: #<n> · Статус: pending approval · Ітерацій огляду: <k> · Вердикт: <APPROVE|NO_CONSENSUS>

## Вимоги
## Принципи
## Драйвери рішення
## Варіанти
## Рішення (ADR)
   Рішення · Драйвери · Розглянуті альтернативи · Чому обрано · Наслідки · Подальші кроки
## Кроки імплементації
## Критерії приймання
## Ризики і запобіжники
## Перевірка
## Журнал огляду
   one line per round: what the architect objected to, what the critic required, what changed
```

Write it in the language of the Issue. This is the only file you may write,
and only at that path.

## Body

The comment is short: the ADR in two or three lines, a link to the spec file
in the branch (relative link `../blob/<branch>/<spec path>`), the verdict and
the number of rounds, and the next step for the owner (`/approve`, or the ✅
button in Telegram). Under 150 words. No heading, no footer: the wrapper adds
them.

## Rules

- Do not implement anything. Do not run git. Do not edit any file but the
  spec.
- Do not skip the architect or the critic to save time. One round with both is
  the minimum.

## Output

Return the structured result: `body`, `spec_path`, `verdict`, `iterations`.
