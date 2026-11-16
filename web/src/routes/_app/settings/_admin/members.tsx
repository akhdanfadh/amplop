import type { ReactElement } from "react";
import { createFileRoute } from "@tanstack/react-router";
import { getListMembersQueryOptions, useListMembers } from "@/api/generated/inbok";
import { userMessage } from "@/api/problem";
import { ReadError } from "@/components/read-error";
import { cachedLoader, loadedFreshMs } from "@/api/cached-read";
import { MemberTable } from "@/features/members/table";
import { accountsAbout, accountsTitle, listReadFailed, membersLead } from "@/features/members/text";
import { SettingsPage, SettingsSection } from "@/features/settings/page";
import { pageName } from "@/features/settings/text";

const unauthorized = 401;

// membersLoader reads what the page shows before it draws.
export const membersLoader = cachedLoader(getListMembersQueryOptions);

export const Route = createFileRoute("/_app/settings/_admin/members")({
  loader: membersLoader,
  component: Members,
  head: () => ({ meta: [{ title: "Members - inbok" }] }),
});

// Members lists every account on this inbok and deactivates or reactivates
// members. The page fills the window and the list scrolls inside it.
function Members(): ReactElement {
  const { session } = Route.useRouteContext();
  const loaded = Route.useLoaderData();
  const list = useListMembers({ query: { staleTime: loadedFreshMs } });
  const members = (list.data ?? loaded).data.members;

  return (
    <SettingsPage title={pageName.members} lead={membersLead} height="fill">
      {/* A failed refresh keeps the list and says why (DESIGN.md "States");
          a 401 is the session-end rule's. */}
      {list.error && list.error.status !== unauthorized && (
        <div className="py-6 text-sm">
          <ReadError
            message={`${listReadFailed} ${userMessage(list.error)}`}
            error={list.error}
            retrying={list.isFetching}
            onRetry={() => void list.refetch()}
          />
        </div>
      )}
      <SettingsSection title={accountsTitle} about={accountsAbout} layout="fill">
        <MemberTable members={members} yourEmail={session.email} />
      </SettingsSection>
    </SettingsPage>
  );
}
