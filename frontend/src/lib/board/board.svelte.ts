import { Events } from "@wailsio/runtime";
import * as Workbench from "../../../bindings/dev.jevido/work/services/workbenchservice.js";
import { BOARD_UPDATED } from "../bridge/events";

export type CardStatus = "todo" | "doing" | "done" | "blocked";

export interface Card {
  id: string;
  runId: string;
  agentId: string;
  title: string;
  note: string;
  status: CardStatus;
}

/** A column of the board, ready to render. */
export interface Column {
  status: CardStatus;
  label: string;
  cards: Card[];
}

const COLUMNS: { status: CardStatus; label: string }[] = [
  { status: "todo", label: "Assigned" },
  { status: "doing", label: "In progress" },
  { status: "done", label: "Done" },
  { status: "blocked", label: "Blocked" },
];

/**
 * The task board.
 *
 * The backend publishes the whole board on every change rather than a delta.
 * It holds a handful of cards and changes a handful of times per run, so a
 * snapshot is both cheaper than reconciling and impossible to desynchronise.
 */
export class TaskBoard {
  cards = $state<Card[]>([]);

  /** Cards grouped into columns, in board order. */
  columns = $derived<Column[]>(
    COLUMNS.map((c) => ({
      ...c,
      cards: this.cards.filter((card) => card.status === c.status),
    })),
  );

  /** True when nothing has been assigned yet. */
  isEmpty = $derived(this.cards.length === 0);

  /** Loads the current board and subscribes to changes. */
  listen(): () => void {
    const off = Events.On(BOARD_UPDATED, (e) => {
      this.cards = (e.data.cards ?? []).map(toCard);
    });

    let cancelled = false;
    Workbench.Board()
      .then((cards) => {
        // A snapshot that arrived while loading is newer than this one.
        if (cancelled || this.cards.length > 0) return;
        this.cards = (cards ?? []).map(toCard);
      })
      .catch(() => {
        // An unreachable backend is already reported by the office; the board
        // simply stays empty.
      });

    return () => {
      cancelled = true;
      off();
    };
  }
}

function toCard(c: {
  id: string;
  runId: string;
  agentId: string;
  title: string;
  note?: string;
  status: string;
}): Card {
  return {
    id: c.id,
    runId: c.runId,
    agentId: c.agentId,
    title: c.title,
    note: c.note ?? "",
    status: asStatus(c.status),
  };
}

function asStatus(value: string): CardStatus {
  switch (value) {
    case "doing":
    case "done":
    case "blocked":
      return value;
    default:
      return "todo";
  }
}
