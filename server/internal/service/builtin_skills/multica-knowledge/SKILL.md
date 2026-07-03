---
name: multica-knowledge
description: "Use when capturing, linking, querying, or curating durable knowledge in the Multica knowledge graph: people, organizations, concepts, claims, events, works, and their typed relationships. Covers search-first dedup, provenance requirements, the proposed/confirmed trust gate, fact supersedence, multi-hop graph/path queries, and what agents must not do."
user-invocable: false
allowed-tools: Bash(multica *)
---

# Multica Knowledge Graph

## Quick start

The knowledge graph is the workspace's durable memory. Knowledge you write here survives this task, this session, and this runtime — it is readable by every other agent and by the user, forever. Prefer it over any runtime-private memory.

```bash
multica knowledge search "<query>" --output json
multica knowledge node get <slug-or-id> --output json
multica knowledge graph <slug-or-id> --hops 2 --output json
```

Search is hybrid when the workspace has an embedding provider configured:
lexical (slug/title/alias) plus semantic similarity, fused. The response's
`"semantic"` field reports whether the semantic arm ran. Phrase queries by
meaning, not just exact tokens.

## Retrieve before you research

At the start of a task that involves research, people, organizations, or
prior decisions, query the graph before reaching for the web: run
`multica knowledge search` on the task's key terms and `multica knowledge
graph` on the best hit. Knowledge already captured — by you, another agent,
or the user — is more trustworthy than a fresh search and often already
answers the question.

## Core model

- A **node** is an entity: `person`, `organization`, `brand`, `concept`, `idea`, `claim`, `event`, `work`, `technology`, `market`, `place`, or `note`. Its markdown `content` is the entity's wiki page; `summary` is the one-line tooltip.
- An **edge** is a typed, directed fact between two endpoints, e.g. `works_at`, `authored`, `influenced`, `cites`, `contradicts`, `part_of`, `mentions`. Endpoints are knowledge nodes, or internal entities (`issue`, `project`, `agent`, `member`) referenced by UUID.
- A fact is a **claim with evidence**, not a row you overwrite. Re-asserting a known fact affirms it (the response carries `"affirmed": true`); it never duplicates. Facts are closed and superseded, never edited in place.
- Everything you write lands as `proposed` until a member confirms it. This is by design — do not try to work around it.

## Search first, always

Before creating a node, search. Duplicates poison the graph.

```bash
multica knowledge search "Jane Doe" --output json
```

If a matching node exists, use its slug. If `node add` returns HTTP 409 with `candidates`, one of those candidates is almost certainly the entity you mean — use it. Only pass `--confirm-new` when you have checked the candidates and they are genuinely different entities.

```bash
multica knowledge node add --kind person --title "Jane Doe" \
  --summary "CTO of Acme; writes on developer tooling" \
  --alias "J. Doe" --output json
```

## Provenance is mandatory for extracted facts

Every fact you derive from a document, page, or conversation must carry its source. A fact without provenance cannot be trusted, re-verified, or defended later.

```bash
multica knowledge edge add --src jane-doe --dst acme-corp --predicate works_at \
  --source-url https://acme.example/team --note "listed as CTO on team page" --output json
```

Re-asserting with a new source strengthens the fact (evidence accumulates, confidence rises). If you find a source that contradicts an existing fact, attach contradicting evidence instead of deleting anything — a member resolves it:

```bash
multica knowledge edge add --src jane-doe --dst beta-inc --predicate works_at \
  --source-url https://beta.example/about --output json
```

For single-valued predicates like `works_at`, your conflicting claim is created as `proposed` alongside the live fact and surfaces in the review queue; the response lists the conflict under `conflicts_pending_review`. Do not attempt `--supersede`; it is member-only.

## Linking work items into the graph

Connect issues and projects to the knowledge they touch so the graph and the work stay one fabric:

```bash
multica knowledge edge add --src-type issue --src <issue-uuid> --dst jane-doe --predicate mentions --output json
multica knowledge edge add --src-type issue --src <issue-uuid> --dst some-claim --predicate evidence_for --output json
```

## Querying: multi-hop exploration

```bash
multica knowledge graph jane-doe --hops 3 --output json
multica knowledge path jane-doe quantum-computing --output json
```

`graph` returns a bounded neighborhood (`truncated: true` means coverage was capped — say so if you report on it). `path` answers "how are these two things related?" with the shortest connecting chain.

## When to capture

Capture when you encounter durable, reusable knowledge: a person/organization relevant to the user's work, a claim from research worth remembering, an author/work and what it argues, an event and its participants, or an explicit user ask ("remember this", "add this to the knowledge base"). Write the fact once, with provenance, at the moment you have the source in front of you.

Do not capture: secrets or credentials, transient task state, speculation without a source, or bulk dumps of documents (capture the entities and claims, not the raw text).

## Review and curation (members)

Agent-written material waits in the review queue:

```bash
multica knowledge review list --output json
multica knowledge review approve edge <edge-id> --output json
multica knowledge review reject node <slug-or-id> --output json
```

## Extraction autopilot recipe

To mine issues/comments/research into proposed knowledge on a schedule, create an autopilot whose prompt instructs the agent to extract entities and facts with provenance (see multica-autopilots for the autopilot contract):

```bash
multica autopilot create --title "Knowledge extraction" \
  --description "Review recent issues and comments in this workspace. For each durable entity or claim, search the knowledge graph first, then capture missing nodes/edges with multica knowledge, always with --source-url or a --note naming the issue. Never use --confirm-new without checking candidates." \
  --agent <agent-name> --mode run_only --output json
multica autopilot trigger-add <autopilot-id> --kind schedule --cron "0 6 * * *" --timezone UTC --output json
```

## What agents must not do

- No `--supersede`, no `edge close`, no node `delete`/`merge`, no status changes — these are member-only curation actions and the API rejects them.
- Never delete or rewrite existing knowledge to "fix" it; add evidence or a proposed competing fact and let review decide.
- Do not create nodes for trivial or one-off strings; a node is an entity someone would look up later.

Side effects: `node add`, `edge add`, and evidence writes are durable workspace mutations visible to every member and agent. They are safe to perform when the task's research justifies them; they are not scratch space.

More source-backed details: `references/knowledge-source-map.md`.
