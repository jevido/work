# Template

Unedited template -- replace this line with what this agent owns.

Copy this folder to make an agent:

    cp -r _template ada

The folder name is the agent's id, and Work capitalises it for the name on the
desk -- `ada` becomes Ada. Folders starting with `_` or `.` are skipped, which
is why this one is not on the team. Change the heading above to the new name.

Then replace the line under it with the agent itself: what they own, how they
work, what they push back on.

**The first line of prose is the blurb Anton routes on.** It is the only thing
he knows about this agent when he decides who gets a task, so spend it on the
specialty rather than on a greeting -- and edit it, or the placeholder above is
what he reads.

This file is read fresh every time the agent is given work, so an edit lands on
the next task rather than the next restart. Keep it short: every task pays for
it in tokens.

Optional, beside this file:

- `avatar.webp` or `avatar.png`, drawn at the desk in the office.
- `skills/`, one folder or `.md` file per skill. A symlink into a shared
  skills folder counts, which is the cheap way to share one between agents.
  Each one is read and handed to the agent along with this file, every time
  they are given work -- so a skill is instructions that take effect, and
  every task pays for it in tokens the same way this file does.
