import type { ReactElement } from "react";
import type { UseQueryResult } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { Button } from "@/components/ui/button";
import { useGetHealth, type GetHealthQueryResult } from "@/api/generated/inbok";
import { userMessage } from "@/api/problem";
import type { ApiError } from "@/api/transport";
import { ProbeForm } from "@/features/probe/form";

export const Route = createFileRoute("/probe")({ component: Probe });

function Probe(): ReactElement {
  // Checks once on load, again on click, and when the tab regains focus.
  const health = useGetHealth();

  return (
    <main className="flex flex-col items-start gap-4 p-8">
      <h1 className="text-3xl font-bold">inbok</h1>
      <Button type="button" disabled={health.isFetching} onClick={() => void health.refetch()}>
        Check health
      </Button>
      <output>{show(health)}</output>
      <ProbeForm />
    </main>
  );
}

// "Checking..." while a request (or a retry) runs, so an earlier "ok" never
// shows as current. Then "ok", or the error, e.g. "Bad Gateway".
function show(health: UseQueryResult<GetHealthQueryResult, ApiError>): string | undefined {
  if (health.isFetching) return "Checking...";
  if (health.isError) return userMessage(health.error);
  return health.data?.data.status;
}
