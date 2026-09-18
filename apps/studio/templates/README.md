# templates

What Work writes into a config folder that does not have it yet. Embedded in the
binary, because a release is one file somebody puts on their PATH — anything it
needs on first run has to already be inside it.

```
agents/anton/       Guaranteed. The coordinator; without one there is no team.
agents/jared/       Guaranteed. Leads Orientation and Isolation, and never implements.
agents/chris/       Starter. Interfaces: components, interaction, accessibility.
agents/dennis/      Starter. Backends: data models, interfaces, boundaries.
agents/jeff/        Starter. Whole stacks: builds, deployment, what got slow.
agents/_template/   Work's own documentation. Copy it to add somebody.
```

## Guaranteed, starter, template

Three different lifetimes, and the difference is the whole design:

**Guaranteed** agents are written on every start. Delete Anton and he is back
next launch, because `Scan` cannot assemble a team without a coordinator and
Orientation and Isolation would otherwise be led by the agent whose entire
prompt is about routing work.

**Starter** colleagues are written **once**, into a folder that has never had
agents in it. Deleting one is how somebody says they did not want it, and it
stays deleted. They exist because a fresh install used to have nobody to
delegate to: a coordinator and an adviser, and an adviser is never handed a
step, so the first thing anybody saw was a team that could only answer for
itself.

**The template** is rewritten every start. It is Work's documentation rather
than anybody's agent, and a stale copy of it describes a folder layout that may
no longer be the one Work reads. Anyone who wanted to keep an edited version has
already copied it, which is what it is for.

An existing file is never overwritten in the first two cases. The moment a
`PERSONALITY.md` is on disk it is the user's document, and an agent whose
instructions are silently rewritten under them is an agent nobody can tune.

## Writing one

The first line of prose is the blurb Anton routes on — it is all he knows about
an agent when he decides who gets a task, so it says what they are for. Below
that, write what a person needs in order to make the agent theirs.

These are deliberately not system prompts. The personality file is read back and
appended to the prompt on every turn, so a file that repeated the prompt would
hand Claude the same instructions twice for the life of the folder. Anton's and
Jared's behaviour is built in; their files say what to change, not what they
already do.

Keep them project-neutral. They land in a stranger's config folder and describe
agents that will work on a codebase nobody here has seen, so they name habits
and judgement, not paths or frameworks.
