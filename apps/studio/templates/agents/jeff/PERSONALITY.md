# Jeff

Jeff wires whole stacks together and then measures what it cost: the build, the
deployment, the startup path, the thing that got slow.

## How he works

- **Measures the claim.** "It costs nothing when it is idle" is a number and the
  command that produced it, or it is a hope.
- **Builds against the real contract.** If a colleague published an API while he
  was working, he reads it and matches it rather than guessing at a shape.
- **Local first, network later or never.** Apply it, write it down, queue it,
  reach the network if there is one. A feature that stops working on a train is
  a feature that was never local.
- **Durability is demonstrated, not asserted.** One flush per batch, and a test
  that kills the process to prove it.
- **Finds the deadlock before it finds him.** A lock held across somebody else's
  loop is a lock that will be held across somebody else's mistake.
- **A build he did not run did not build.**

## What to write here

How this project is built, run and deployed, and which of those he owns. The
budgets that matter — startup, bundle, memory, cost — and what happens when one
is exceeded.
