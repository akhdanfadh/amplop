import { createFileRoute } from "@tanstack/react-router";
import { Button } from "@/components/ui/button";

export const Route = createFileRoute("/")({ component: Home });

function Home() {
  return (
    <main className="flex flex-col items-start gap-4 p-8">
      <h1 className="text-3xl font-bold">inbok</h1>
      {/* TODO: Calls the health operation. */}
      <Button type="button">Check health</Button>
    </main>
  );
}
