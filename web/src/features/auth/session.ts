// A session read this recent counts as fresh: a page that mounts right
// after its route guard read the session uses that read instead of
// sending another, which would show "Checking your session" for nothing.
export const sessionFreshMs = 5_000;
