import { useEffect, useRef, useState, type ReactElement } from "react";
import { createFileRoute } from "@tanstack/react-router";
import { getGetSystemEmailQueryOptions, useGetSystemEmail } from "@/api/generated/amplop";
import { userMessage } from "@/api/problem";
import { ReadError } from "@/components/read-error";
import { Button } from "@/components/ui/button";
import { cachedLoader, loadedFreshMs } from "@/api/cached-read";
import { SettingsPage, SettingsSection } from "@/features/settings/page";
import { pageName } from "@/features/settings/text";
import { SettingsForm } from "@/features/system-email/settings-form";
import { Summary, SystemEmailBadge } from "@/features/system-email/summary";
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

// systemEmailLoader reads what the page shows before it draws.
export const systemEmailLoader = cachedLoader(getGetSystemEmailQueryOptions);

export const Route = createFileRoute("/_app/settings/_admin/system-email")({
  loader: systemEmailLoader,
  component: SystemEmail,
  head: () => ({ meta: [{ title: "System email - amplop" }] }),
});

// SystemEmail is the mail server amplop sends its own messages through: the
// saved settings, a form to change them, and a test message.
function SystemEmail(): ReactElement {
  const { session } = Route.useRouteContext();
  const loaded = Route.useLoaderData();
  const systemEmail = useGetSystemEmail({ query: { staleTime: loadedFreshMs } });
  const settings = (systemEmail.data ?? loaded).data.settings;
  // Nothing saved yet opens the form, with no summary to go back to.
  const [editing, setEditing] = useState(!settings);
  // The notice under the summary once a save closed the form.
  const [notice, setNotice] = useState<string>();
  // When a save last may have replaced the settings, which hides an older
  // test result.
  const [savedAt, setSavedAt] = useState(0);
  // Counts "Save and send test" presses that saved.
  const [testRequests, setTestRequests] = useState(0);
  const editRef = useRef<HTMLButtonElement>(null);
  // Set when the form closes without a test to send; Edit settings then
  // takes focus, since the button that had it is gone. With a test, the
  // test form takes it.
  const refocus = useRef(false);

  useEffect(() => {
    if (!refocus.current || editing) return;
    refocus.current = false;
    editRef.current?.focus();
  });

  // close closes the form; next says what takes focus: Edit settings, or
  // the test the save asked for.
  function close(next: "edit" | "test"): void {
    refocus.current = next === "edit";
    setEditing(false);
  }

  return (
    <SettingsPage
      title={pageName.systemEmail}
      aside={<SystemEmailBadge settings={settings} />}
      lead={systemEmailLead}
    >
      {/* A failed refresh keeps the sections and says why (DESIGN.md
          "States"); a 401 is the session-end rule's. */}
      {systemEmail.error && systemEmail.error.status !== unauthorized && (
        <div className="py-6 text-sm">
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
            onSaved={(action) => {
              setNotice(action === "test" ? savedNotice.testing : savedNotice.untested);
              close(action === "test" ? "test" : "edit");
              if (action === "test") setTestRequests((count) => count + 1);
            }}
            onCancel={
              settings &&
              (() => {
                setNotice(undefined);
                close("edit");
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
          sendRequests={testRequests}
        />
      </SettingsSection>
    </SettingsPage>
  );
}
