import { useEffect, useRef, useState, type ReactElement } from "react";
import type { QueryClient } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { getGetSystemEmailQueryOptions, useGetSystemEmail } from "@/api/generated/inbok";
import type { ResponseSystemEmail } from "@/api/generated/model";
import { userMessage } from "@/api/problem";
import { ReadError } from "@/components/read-error";
import { Button } from "@/components/ui/button";
import { loadSignedIn, useSessionEnd } from "@/features/auth/session-end";
import { SettingsPage, SettingsSection } from "@/features/settings/page";
import { pageName } from "@/features/settings/text";
import { SettingsForm } from "@/features/system-email/settings-form";
import { Summary } from "@/features/system-email/summary";
import { TestMessageForm } from "@/features/system-email/test-message-form";
import {
  formText,
  mailServerAbout,
  mailServerTitle,
  readFailed,
  savedNotice,
  systemEmailLead,
  testAbout,
  testTitle,
} from "@/features/system-email/text";

const unauthorized = 401;

export const Route = createFileRoute("/_app/settings/_admin/system-email")({
  loader: loadSystemEmail,
  component: SystemEmail,
  head: () => ({ meta: [{ title: "System email - inbok" }] }),
});

// loadSystemEmail reads the settings before the page draws, so the router
// shows its loading and failure views meanwhile (web/README.md "Routes"),
// for the page at location. A cached read will do, since the page reads
// them again as it opens.
export function loadSystemEmail({
  context,
  location,
}: {
  context: { queryClient: QueryClient };
  location: { href: string };
}): Promise<ResponseSystemEmail> {
  return loadSignedIn(context.queryClient, location.href, () =>
    context.queryClient.query({ ...getGetSystemEmailQueryOptions(), staleTime: "static" }),
  );
}

// SystemEmail is the mail server inbok sends its own messages through: the
// saved settings, a form to change them, and a test message.
function SystemEmail(): ReactElement {
  const { session } = Route.useRouteContext();
  const loaded = Route.useLoaderData();
  const systemEmail = useGetSystemEmail();
  const sessionEnd = useSessionEnd();
  const settings = (systemEmail.data ?? loaded).data.settings;
  // Nothing saved yet opens the form, with no summary to go back to.
  const [editing, setEditing] = useState(!settings);
  // The notice under the summary once a save closed the form.
  const [notice, setNotice] = useState<string>();
  // When a save last may have replaced the settings, which hides an older
  // test result.
  const [savedAt, setSavedAt] = useState(0);
  const editRef = useRef<HTMLButtonElement>(null);
  // Set when the form closes; Edit settings then takes focus, since the
  // button that had it is gone.
  const refocus = useRef(false);

  useEffect(() => {
    sessionEnd(systemEmail.error);
  }, [systemEmail.error, sessionEnd]);

  useEffect(() => {
    if (!refocus.current || editing) return;
    refocus.current = false;
    editRef.current?.focus();
  });

  // close closes the form and gives Edit settings focus.
  function close(): void {
    refocus.current = true;
    setEditing(false);
  }

  return (
    <SettingsPage title={pageName.systemEmail} lead={systemEmailLead}>
      {/* A failed refresh keeps the sections and says why (DESIGN.md
          "States"); a 401 opens sign-in instead. */}
      {systemEmail.error && systemEmail.error.status !== unauthorized && (
        <div className="py-6">
          <ReadError
            message={`${readFailed} ${userMessage(systemEmail.error)}`}
            error={systemEmail.error}
            retrying={systemEmail.isFetching}
            onRetry={() => void systemEmail.refetch()}
          />
        </div>
      )}
      <SettingsSection title={mailServerTitle} about={mailServerAbout}>
        {editing || !settings ? (
          <SettingsForm
            saved={settings}
            focusFirst={settings !== undefined}
            onWrite={() => setSavedAt(Date.now())}
            onSaved={() => {
              setNotice(savedNotice);
              close();
            }}
            onCancel={
              settings &&
              (() => {
                setNotice(undefined);
                close();
              })
            }
          />
        ) : (
          <>
            <Summary settings={settings} />
            <Button
              ref={editRef}
              type="button"
              variant="outline"
              onPress={() => {
                setNotice(undefined);
                setEditing(true);
              }}
            >
              {formText.edit}
            </Button>
          </>
        )}
        {/* Always present, so a screen reader announces the notice that
            fills it. */}
        <output aria-atomic="true" className="text-sm empty:hidden">
          {!editing && notice}
        </output>
      </SettingsSection>

      <SettingsSection title={testTitle} about={testAbout}>
        <TestMessageForm
          key={session.email}
          settings={settings}
          recipient={session.email}
          editing={editing || !settings}
          savedAt={savedAt}
        />
      </SettingsSection>
    </SettingsPage>
  );
}
