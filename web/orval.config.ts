import { defineConfig } from "orval";

// `pnpm generate` writes one TypeScript function per API operation, plus
// the types they use, from the OpenAPI document that Go exports.
export default defineConfig({
  inbok: {
    input: "../api/generated/openapi.json",
    output: {
      target: "src/api/generated/inbok.ts",
      // Delete the old output first, so a removed operation disappears.
      clean: true,
      client: "fetch",
      // Prefix paths with the document's `servers` URL: `/api/v0/health`.
      baseUrl: { getBaseUrlFromSpecification: true },
      override: {
        // Every generated function sends its request through transport.
        mutator: { path: "src/api/transport.ts", name: "transport" },
        // Return the success body itself; failures throw ApiError instead.
        fetch: { includeHttpResponseReturnType: false, forceSuccessResponse: true },
      },
    },
  },
});
