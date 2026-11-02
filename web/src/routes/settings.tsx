import { useEffect, useRef, useState, type ReactElement } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import {
  getGetCurrentSessionQueryKey,
  getGetRegistrationSettingsQueryKey,
  getGetSystemEmailQueryKey,
  useGetCurrentSession,
  useGetRegistrationSettings,
  useGetSystemEmail,
} from "@/api/generated/inbok";
import { userMessage } from "@/api/problem";
import type { ApiError } from "@/api/transport";
import { clearAccountCache } from "@/features/auth/session-cache";
import { RegistrationSection } from "@/features/registration/section";
import { SettingsForm } from "@/features/system-email/settings-form";
import { TestMessageForm } from "@/features/system-email/test-message-form";
import { AccountNav } from "@/components/account-nav";
import { Button } from "@/components/ui/button";

const unauthorized = 401;
const forbidden = 403;

export const Route = createFileRoute("/settings")({
  component: Settings,
  head: () => ({ meta: [{ title: "Settings - inbok" }] }),
});

function Settings(): ReactElement {
  const client = useQueryClient();
  const navigate = useNavigate();
  const headingRef = useRef<HTMLHeadingElement>(null);
  const [savedAt, setSavedAt] = useState(0);
  const [writeExpired, setWriteExpired] = useState(false);
  const current = useGetCurrentSession();
  const systemEmail = useGetSystemEmail();
  const registration = useGetRegistrationSettings();
  const adminReads = [systemEmail, registration];
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
    void client.invalidateQueries({ queryKey: getGetRegistrationSettingsQueryKey() });
    void client.invalidateQueries({ queryKey: getGetCurrentSessionQueryKey() });
  }

  // A failed read without data hides the forms; a failed refetch keeps them.
  const readError =
    expired || denied ? null : (reads.find((read) => !read.data && read.error)?.error ?? null);

  return (
    <>
      <AccountNav account={account} />
      <main className="mx-auto flex w-full max-w-lg flex-col gap-8 px-5 py-8 wrap-anywhere sm:px-8 sm:py-12">
        <h1 ref={headingRef} tabIndex={-1} className="text-3xl font-bold tracking-tight">
          Settings
        </h1>

        {reads.some((read) => read.isPending) && !readError && !denied && (
          <output>Loading settings...</output>
        )}
        {expired && <output>Opening sign-in...</output>}

        {denied && (
          <p>
            Settings are available only to administrators with two-factor authentication.
            {account?.role === "admin" && (
              <>
                {" "}
                <Link
                  to="/security"
                  className="underline underline-offset-4 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground focus-visible:outline-solid"
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
            <p>Could not load settings. {userMessage(readError)}</p>
            {readError.requestId && (
              <p className="text-sm text-muted-foreground">
                Request reference: <code className="break-all">{readError.requestId}</code>
              </p>
            )}
            <Button
              type="button"
              variant="outline"
              className="min-h-11 w-fit border-muted-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground focus-visible:outline-solid"
              disabled={reads.some((read) => read.isFetching)}
              onClick={() => {
                for (const read of reads) void read.refetch();
              }}
            >
              Try again
            </Button>
          </div>
        )}

        {account && systemEmail.data && !expired && !denied && (
          <>
            <section className="flex flex-col gap-6" aria-labelledby="system-email-heading">
              <div className="flex flex-col gap-2">
                <h2 id="system-email-heading" className="text-xl font-semibold">
                  System email
                </h2>
                <p>
                  The mail server inbok sends its own messages through. Members&apos; mailboxes are
                  separate.
                </p>
              </div>
              <SettingsForm
                saved={settings}
                onCheck={() => systemEmail.refetch()}
                onSaved={() => setSavedAt(Date.now())}
                onAccessError={handleAccessError}
              />
            </section>

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

            {/* A failed registration read must not hide system email, which
                opening registration needs. */}
            {registration.data && (
              <RegistrationSection
                settings={registration.data.data}
                checkedAt={registration.dataUpdatedAt}
                emailTestedAt={settings?.testedAt ?? undefined}
                onAccessError={handleAccessError}
              />
            )}
          </>
        )}
      </main>
    </>
  );
}
