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
import { Button } from "@/components/ui/button";

const unauthorized = 401;
const forbidden = 403;

export const Route = createFileRoute("/_app/settings/_admin/registration")({
  component: Registration,
  head: () => ({ meta: [{ title: "Registration - inbok" }] }),
});

// Registration is whether visitors can create accounts. Opening it needs
// tested system email, so the page reads those settings too.
function Registration(): ReactElement {
  const client = useQueryClient();
  const navigate = useNavigate();
  const headingRef = useRef<HTMLHeadingElement>(null);
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
    <main className="mx-auto flex w-full max-w-lg flex-col gap-8 px-5 py-8 wrap-anywhere sm:px-8 sm:py-12">
      <h1 ref={headingRef} tabIndex={-1} className="text-3xl font-bold tracking-tight">
        Registration
      </h1>

      {reads.some((read) => read.isPending) && !readError && !denied && (
        <output>Loading registration settings...</output>
      )}
      {expired && <output>Opening sign-in...</output>}

      {denied && (
        <p>
          Registration settings are available only to administrators with two-factor authentication.
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
          <p>Could not load registration settings. {userMessage(readError)}</p>
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

      {account && systemEmail.data && registration.data && !expired && !denied && (
        <RegistrationSection
          settings={registration.data.data}
          checkedAt={registration.dataUpdatedAt}
          emailTestedAt={settings?.testedAt ?? undefined}
          onAccessError={handleAccessError}
        />
      )}
    </main>
  );
}
