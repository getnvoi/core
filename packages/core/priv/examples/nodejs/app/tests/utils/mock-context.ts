/**
 * ## THERE IS NO CONTEXT TO MOCK
 *
 * This exported `createMockCloudflareContext`, which built a fake D1 statement
 * chain, a fake queue binding and a fake Durable Object namespace — sixty lines
 * standing in for a platform.
 *
 * Off Cloudflare, a loader takes no context: the database is a file the module
 * opens, the queue is a table, and both are real in a test. A test opens an
 * in-memory database and the code under test is the code that runs.
 *
 * See `app/tests/setup.ts`, which requires `DATABASE_URL` — a scratch
 * database, never a default, because a default runs against whatever is on the
 * machine.
 */

export function createMockUser() {
  return {
    id: "test-user-id",
    email: "test@example.com",
    login: "testuser",
    name: "Test User",
    avatarUrl: "https://example.com/avatar.jpg",
  };
}
