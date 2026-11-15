import { QueryClient } from "@tanstack/react-query";
import { expect, it } from "vite-plus/test";
import { clearAccountCache } from "@/features/auth/session-cache";
import { currentMail, mailData } from "./conversations";

it("ends a member's mail with their session", () => {
  const client = new QueryClient();
  const { conversations } = mailData(client);
  expect(currentMail(client)?.conversations).toBe(conversations);

  clearAccountCache(client);

  expect(currentMail(client)).toBeUndefined();
  expect(conversations.status).toBe("cleaned-up");
  client.clear();
});
