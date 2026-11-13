import { createFileRoute, redirect } from "@tanstack/react-router";
import { connectSearch } from "@/features/connected-accounts/search";

// /connected-accounts is an entry address: the server sends the browser
// here after Google's consent page, with how the connection ended, so it
// stays and opens Connected accounts, outcome and all, wherever settings
// put it.
export const Route = createFileRoute("/connected-accounts")({
  validateSearch: connectSearch,
  beforeLoad: ({ search }) => {
    throw redirect({ to: "/settings/connected-accounts", search, replace: true });
  },
});
