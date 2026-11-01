import * as z from "zod";
import { validationMessage } from "@/api/validation";

// Zod applies some settings when a schema is created, and the app creates
// schemas as their modules load. src/router.tsx imports this module first,
// and vite.config.ts keeps modules running in import order, so these
// settings come before any schema.

// Every Zod check in the app, e.g. a form's, reports in the app's words.
// jitless stops Zod from testing whether it may compile code with
// Function(), which the server's Content-Security-Policy forbids and the
// browser logs as a violation.
z.config({ customError: validationMessage, jitless: true });
