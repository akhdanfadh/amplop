import { useMemo } from "react";
import { useLiveQuery } from "@tanstack/react-db";
import { ChangePlace } from "@/api/generated/model/changePlace.zod";
import type { MailData, PlaceCount } from "./conversations";
import { labelTree, usePlaceCounts, type LabelNode } from "./place-counts";
import { isViewPlace, type Place } from "./places";

// Label is one of the member's labels as the menus show it: its
// place (its id), its account, its own name, its name with its parents'
// ("Work/Projects"), and how deep it sits under them, 0 at the top.
export type Label = {
  place: string;
  accountId: number;
  name: string;
  path: string;
  depth: number;
};

// labelsOf returns the labels in counts, account by account in the order
// counts lists them, each account's as the sidebar's tree orders them:
// under their parents, each level in name order.
export function labelsOf(counts: readonly PlaceCount[]): Label[] {
  const accounts = [...new Set(counts.map((count) => count.accountId))];
  const labels: Label[] = [];
  const add = (accountId: number, nodes: readonly LabelNode[], depth: number) => {
    for (const node of nodes) {
      labels.push({ place: node.place, accountId, name: node.name, path: node.path, depth });
      add(accountId, node.children, depth + 1);
    }
  };
  for (const accountId of accounts) add(accountId, labelTree(counts, accountId), 0);
  return labels;
}

// useLabels returns the member's labels, as labelsOf orders them.
export function useLabels(): Label[] {
  const counts = usePlaceCounts();
  return useMemo(() => labelsOf(counts), [counts]);
}

// useLabelsIn returns the labels of mail's places, as useLabels does, or
// none without mail: it never starts the collections, so a view that
// outlives the member's mail, e.g. the reading pane as sign-in opens,
// reads nothing again.
export function useLabelsIn(mail: MailData | undefined): Label[] {
  const { data: counts } = useLiveQuery({
    query: (q) =>
      mail ? q.from({ p: mail.placeCounts }).select(({ p }) => ({ ...p })) : undefined,
  });
  return useMemo(() => labelsOf(counts ?? []), [counts]);
}

// The views a change can take a conversation out of, from the contract
// (ChangePlace), so a move is offered only where the server takes it.
const changeViews: readonly string[] = ChangePlace.options[0].options;

// canMoveFrom reports whether a conversation can be moved out of place, a
// list's: a view a change can name, e.g. the Inbox, or a label. Sent, All
// mail, and Starred follow from other places, so nothing moves out of
// them.
export function canMoveFrom(place: Place): boolean {
  return isViewPlace(place) ? changeViews.includes(place) : true;
}
