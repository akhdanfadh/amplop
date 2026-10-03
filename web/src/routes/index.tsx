import { useState } from "react";
import { createFileRoute } from "@tanstack/react-router";
import { Button } from "@/components/ui/button";
import { getHealth } from "@/api/generated/inbok";
import { ApiError } from "@/api/transport";

export const Route = createFileRoute("/")({ component: Home });

function Home() {
  const [pending, setPending] = useState(false);
  const [result, setResult] = useState("");

  // Shows "ok", or the error, e.g. "Bad Gateway" while Go is down.
  async function checkHealth() {
    setPending(true);
    try {
      const { data } = await getHealth();
      setResult(data.status);
    } catch (err) {
      if (!(err instanceof ApiError)) throw err;
      setResult(describe(err));
    } finally {
      setPending(false);
    }
  }

  return (
    <main className="flex flex-col items-start gap-4 p-8">
      <h1 className="text-3xl font-bold">inbok</h1>
      <Button type="button" disabled={pending} onClick={() => void checkHealth()}>
        Check health
      </Button>
      <output>{result}</output>
    </main>
  );
}

// "Not Found: route not found", or the title alone when there is no detail.
function describe(err: ApiError) {
  const { title, detail } = err.problem;
  return [title, detail].filter(Boolean).join(": ");
}
