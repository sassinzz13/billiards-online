import { HttpErrorResponse } from '@angular/common/http';

/**
 * Extracts a user-safe message from a failed ApiClient call, falling back to a generic one.
 *
 * The server's error body is `{ error: { code, message } }` (platform convention across every
 * feature — see e.g. internal/auth/handler.go's badRequest/internalError helpers), and its message
 * is already written for a human and deliberately vague where vagueness matters (login errors are
 * identical for a wrong password and an unknown account, so this cannot be used to enumerate
 * registered addresses). This function only unwraps that shape; it never invents detail the server
 * did not send (§42).
 *
 * This is the third call site to need this exact unwrap (auth, profile, rooms), which is the
 * threshold this project's conventions ask for before extracting a shared helper — see MEMORY.md §6.
 */
export function apiErrorMessage(err: unknown, fallback: string): string {
  if (err instanceof HttpErrorResponse) {
    const body = err.error as { error?: { message?: string } } | null;
    if (body?.error?.message) {
      return body.error.message;
    }
    if (err.status === 0) {
      return 'Cannot reach the server. Check your connection.';
    }
  }
  return fallback;
}
