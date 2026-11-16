import type { ReactElement } from "react";
import { createFileRoute } from "@tanstack/react-router";
import {
  getGetRegistrationSettingsQueryOptions,
  useGetRegistrationSettings,
} from "@/api/generated/inbok";
import { userMessage } from "@/api/problem";
import { ReadError } from "@/components/read-error";
import { cachedLoader, loadedFreshMs } from "@/api/cached-read";
import { RegistrationBadge, RegistrationSection } from "@/features/registration/section";
import { readFailed, registrationLead } from "@/features/registration/text";
import { SettingsPage } from "@/features/settings/page";
import { pageName } from "@/features/settings/text";

const unauthorized = 401;

// registrationLoader reads what the page shows before it draws.
export const registrationLoader = cachedLoader(getGetRegistrationSettingsQueryOptions);

export const Route = createFileRoute("/_app/settings/_admin/registration")({
  loader: registrationLoader,
  component: Registration,
  head: () => ({ meta: [{ title: "Registration - inbok" }] }),
});

// Registration is whether visitors can create their own accounts.
function Registration(): ReactElement {
  const loaded = Route.useLoaderData();
  const read = useGetRegistrationSettings({ query: { staleTime: loadedFreshMs } });
  const settings = (read.data ?? loaded).data;

  return (
    <SettingsPage
      title={pageName.registration}
      aside={<RegistrationBadge status={settings.status} />}
      lead={registrationLead}
    >
      {/* A failed refresh keeps the section and says why (DESIGN.md
          "States"); a 401 is the session-end rule's. */}
      {read.error && read.error.status !== unauthorized && (
        <div className="py-6 text-sm">
          <ReadError
            message={`${readFailed} ${userMessage(read.error)}`}
            error={read.error}
            retrying={read.isFetching}
            onRetry={() => void read.refetch()}
          />
        </div>
      )}
      <RegistrationSection settings={settings} />
    </SettingsPage>
  );
}
