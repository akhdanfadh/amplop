import { useEffect, useRef, useState, type ReactElement } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import {
  getGetCurrentSessionQueryKey,
  getGetSystemEmailQueryKey,
  useGetCurrentSession,
  useGetSystemEmail,
} from "@/api/generated/inbok";
import { userMessage } from "@/api/problem";
import type { ApiError } from "@/api/transport";
import { clearAccountCache } from "@/features/auth/session-cache";
import { SettingsForm } from "@/features/system-email/settings-form";
import { TestMessageForm } from "@/features/system-email/test-message-form";
import { Button } from "@/components/ui/button";

const unauthorized = 401;
const forbidden = 403;

export const Route = createFileRoute("/_app/settings/_admin/system-email")({
  component: SystemEmail,
  head: () => ({ meta: [{ title: "System email - inbok" }] }),
});

// SystemEmail is the mail server inbok sends its own messages through,
// with a test message.
function SystemEmail(): ReactElement {
  const client = useQueryClient();
  const navigate = useNavigate();
  const headingRef = useRef<HTMLHeadingElement>(null);
  const [savedAt, setSavedAt] = useState(0);
  const [writeExpired, setWriteExpired] = useState(false);
  const current = useGetCurrentSession();
  const systemEmail = useGetSystemEmail();
  const adminReads = [systemEmail];
  const reads = [current, ...adminReads];
  const expired = writeExpired || reads.some((read) => read.error?.status === unauthorized);
  // Access can be lost after the page loaded, so a 403 hides the forms even
  // with settings already read.
  const denied = !expired && adminReads.some((read) => read.error?.status === forbidden);
  const account = current.data?.data;
  const settings = systemEmail.data?.data.settings;

  useEffect(() => {
    headingRef.current?.focus();
  }, []);
  useEffect(() => {
    if (!expired) return;
    clearAccountCache(client);
    void navigate({ to: "/sign-in", replace: true });
  }, [expired, client, navigate]);

  // Ends the page on a lost session and rereads access on a 403 from a write.
  function handleAccessError(err: ApiError): void {
    if (err.status === unauthorized) setWriteExpired(true);
    if (err.status !== forbidden) return;
    void client.invalidateQueries({ queryKey: getGetSystemEmailQueryKey() });
    void client.invalidateQueries({ queryKey: getGetCurrentSessionQueryKey() });
  }

  // A failed read without data hides the forms; a failed refetch keeps them.
  const readError =
    expired || denied ? null : (reads.find((read) => !read.data && read.error)?.error ?? null);

  return (
    <main className="mx-auto flex w-full max-w-lg flex-col gap-8 px-5 py-8 wrap-anywhere sm:px-8 sm:py-12">
      <h1 ref={headingRef} tabIndex={-1} className="text-3xl font-bold tracking-tight">
        System email
      </h1>
      <p>
        The mail server inbok sends its own messages through. Members&apos; mailboxes are separate.
      </p>

      {reads.some((read) => read.isPending) && !readError && !denied && (
        <output>Loading system email settings...</output>
      )}
      {expired && <output>Opening sign-in...</output>}

      {denied && (
        <p>
          System email settings are available only to administrators with two-factor authentication.
          {account?.role === "admin" && (
            <>
              {" "}
              <Link
                to="/settings/security"
                className="underline underline-offset-4 focus-visible:focus-ring"
              >
                Set up two-factor authentication
              </Link>
              .
            </>
          )}
        </p>
      )}
      {readError && (
        <div className="flex flex-col gap-3" role="alert">
          <p>Could not load system email settings. {userMessage(readError)}</p>
          {readError.requestId && (
            <p className="text-sm text-muted-foreground">
              Request reference: <code className="break-all">{readError.requestId}</code>
            </p>
          )}
          <Button
            type="button"
            variant="outline"
            className="w-fit"
            isDisabled={reads.some((read) => read.isFetching)}
            onPress={() => {
              for (const read of reads) void read.refetch();
            }}
          >
            Try again
          </Button>
        </div>
      )}

      {account && systemEmail.data && !expired && !denied && (
        <>
          <SettingsForm
            saved={settings}
            onCheck={() => systemEmail.refetch()}
            onSaved={() => setSavedAt(Date.now())}
            onAccessError={handleAccessError}
          />

          <section className="flex flex-col gap-6" aria-labelledby="test-message-heading">
            <div className="flex flex-col gap-2">
              <h2 id="test-message-heading" className="text-xl font-semibold">
                Test message
              </h2>
              <p>
                Sends a message with the saved settings. A pass marks them as tested. A failure
                marks them as untested, even when only the recipient is wrong.
              </p>
            </div>
            <TestMessageForm
              key={account.email}
              settings={settings}
              recipient={account.email}
              savedAt={savedAt}
              onAccessError={handleAccessError}
            />
          </section>
        </>
      )}
    </main>
  );
}
