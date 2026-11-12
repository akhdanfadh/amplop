import {
  createContext,
  useContext,
  useRef,
  useEffect,
  useState,
  useCallback,
  useMemo,
  type ReactNode,
} from "react";
import { useQueryClient, type UseQueryResult } from "@tanstack/react-query";
import { Navigate, trimPathRight, useRouterState } from "@tanstack/react-router";
import { useGetSetupStatus, getGetSetupStatusQueryKey } from "@/api/generated/inbok";
import { userMessage } from "@/api/problem";
import type { ApiError } from "@/api/transport";
import type { ResponseSetupStatus } from "@/api/generated/model";
import { Button } from "@/components/ui/button";

// SetupState is the shared installation status and setup actions.
type SetupState = {
  status: UseQueryResult<ResponseSetupStatus, ApiError>;
  attempt: () => void;
  complete: () => Promise<void>;
  completedHere: boolean;
};
const SetupContext = createContext<SetupState | null>(null);

// useSetup reads installation state owned by the application gate.
export function useSetup(): SetupState {
  const state = useContext(SetupContext);
  if (!state) throw new Error("SetupGate is missing.");
  return state;
}

// SetupGate restricts all pages to setup until the backend reports completion.
export function SetupGate({ children }: { children: ReactNode }) {
  const client = useQueryClient();
  const isSetup = useRouterState({
    // The URL can change before the previous page has finished unmounting.
    select: (state) =>
      trimPathRight(state.location.pathname).toLowerCase() === "/setup" &&
      state.matches.at(-1)?.routeId === "/setup",
  });
  const status = useGetSetupStatus({
    // Keep setup reachable even when the browser reports offline.
    query: { staleTime: Infinity, networkMode: "always" },
  });
  // A later status check can confirm an attempt whose response was lost.
  const [attemptedHere, setAttemptedHere] = useState(false);
  const completedHere = attemptedHere && status.data?.data.status === "done";
  const heading = useRef<HTMLHeadingElement>(null);
  useEffect(() => {
    if (status.isError) heading.current?.focus();
  }, [status.isError]);

  const attempt = useCallback(() => setAttemptedHere(true), []);
  const complete = useCallback(async (): Promise<void> => {
    // An older status response must not overwrite a successful setup claim.
    await client.cancelQueries({ queryKey: getGetSetupStatusQueryKey(), exact: true });
    setAttemptedHere(true);
    client.setQueryData(getGetSetupStatusQueryKey(), { data: { status: "done" } });
  }, [client]);
  const value = useMemo(
    () => ({ status, attempt, complete, completedHere }),
    [status, attempt, complete, completedHere],
  );

  // Failed rechecks keep the form mounted so entered values survive retry.
  if (!status.data)
    return (
      <main className="mx-auto flex w-full max-w-lg flex-col gap-6 px-5 py-8 wrap-anywhere sm:px-8 sm:py-12">
        <h1 ref={heading} tabIndex={-1} className="text-3xl font-bold tracking-tight">
          inbok
        </h1>
        {status.isPending && <output>Checking setup status...</output>}
        {status.isError && (
          <>
            <div className="flex flex-col gap-2" role="alert">
              <p>Could not check setup status. {userMessage(status.error)}</p>
              {status.error.requestId && (
                <p className="text-sm text-muted-foreground">
                  Request reference: <code className="break-all">{status.error.requestId}</code>
                </p>
              )}
            </div>
            <Button
              type="button"
              variant="outline"
              isDisabled={status.isFetching}
              onPress={() => void status.refetch()}
            >
              {status.isFetching ? "Checking setup status..." : "Try again"}
            </Button>
          </>
        )}
      </main>
    );

  const done = status.data.data.status === "done";
  if (!done && !isSetup) return <Navigate to="/setup" replace />;
  if (done && isSetup) return <Navigate to="/" replace />;

  return <SetupContext.Provider value={value}>{children}</SetupContext.Provider>;
}
