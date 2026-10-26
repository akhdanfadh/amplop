import { defineConfig } from "orval";

// `pnpm generate` writes one TypeScript function per API operation, a
// TanStack Query hook for it, and the types they use with a Zod schema for
// each, from the OpenAPI document that Go exports.
export default defineConfig({
  inbok: {
    input: "../api/generated/openapi.json",
    output: {
      target: "src/api/generated/inbok.ts",
      // Delete the old output first, so a removed operation disappears.
      clean: true,
      // Hooks such as useGetHealth(), on top of plain fetch functions.
      client: "react-query",
      httpClient: "fetch",
      // Types as Zod schemas, which check a response at runtime.
      schemas: { path: "src/api/generated/model", type: "zod" },
      // Prefix paths with the document's `servers` URL: `/api/v0/health`.
      baseUrl: { getBaseUrlFromSpecification: true },
      override: {
        // Every generated function sends its request through transport.
        mutator: { path: "src/api/transport.ts", name: "transport" },
        // Return the success body itself; failures throw ApiError instead.
        fetch: {
          includeHttpResponseReturnType: false,
          forceSuccessResponse: true,
          // Required by the next option. Orval's own check never runs with
          // a mutator; transport runs it instead.
          runtimeValidation: true,
        },
        // Pass each response's schema to transport, which checks the body.
        includeZodSchemaInArguments: true,
        // Pinned, so the output never depends on which Zod Orval detects:
        // its own dependencies include Zod 3.
        zod: { version: 4 },
      },
    },
  },
});
