# ops

The merge. One implementation, run by the desktop app and by the sync server.

A workspace is an operation log rather than a document: every edit is an op with
a clock and an actor, the server keeps them in order, and the document anybody
sees is what folding that log produces. This package is the fold.

It is here, and not in either program, because there is no correct side when two
merges disagree. The desktop app merges what it has locally so that editing
works with the network down; the server merges so that a viewer with no document
model can be handed a result. Those are the same rules by definition, and a
second implementation of them is a bug that shows up as two people looking at
different documents with no way to tell which one is wrong.

The web viewer needs the same fold in the browser and does **not** get a second
copy: it asks the server for the merged document. `apps/website/src/lib/ops.ts`
exists only for what a page needs to render — this file stays normative.

Nothing here reaches the network, touches a database or knows what a workspace
is for. It takes ops and gives back a tree.
