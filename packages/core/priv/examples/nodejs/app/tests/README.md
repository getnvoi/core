# Testing Documentation

## Overview

Basic integration tests that assert all routes return expected status codes (200, 302, etc.).

## Test Structure

```
app/tests/
├── setup.ts                    # Test setup & globals
├── utils/
│   ├── test-helpers.ts         # Request/form helpers
│   └── mock-context.ts         # Mock Cloudflare context & users
└── routes/
    ├── public.test.ts          # Public routes (home)
    ├── app.test.ts             # Authenticated routes
    ├── system.test.ts          # System routes
    └── oauth.test.ts           # OAuth routes
```

## Running Tests

```bash
# Run tests in watch mode
npm test

# Run tests once
npm run test:run

# Run tests with UI
npm run test:ui
```

## Test Coverage

### Public Routes
- ✅ `GET /home` returns 200
- ✅ Returns provider list
- ✅ Returns null user when not authenticated

### App Routes
- ✅ `GET /app` returns 200 with valid session
- ✅ Loads user data correctly
- ✅ Loads user jobs from database

### System Routes
- ✅ `GET /system` returns component successfully

### OAuth Routes
- ✅ `GET /oauth/github` returns 302 redirect
- ✅ `GET /oauth/google` returns 302 redirect
- ✅ `GET /oauth/auth0` returns 302 redirect

## Mock Utilities

### `createMockRequest(url, options)`
Creates a mock Request object for testing loaders/actions.

```typescript
const request = createMockRequest("/home", {
  method: "POST",
  headers: { "Content-Type": "application/json" },
});
```

### `testDb()` and `truncate()`

The REAL database, on the `DATABASE_URL` `setup.ts` requires. There is no
context to mock and no emulator to boot: off Cloudflare the database is a file
this process opens, so a test opens it and the code under test is the code that
ships.

```typescript
import { testDb, truncate } from "../helpers/db";

beforeEach(() => {
  db = testDb();
  truncate();
});
```

Migrations run on the first open, so a fresh temporary file arrives with the
schema already on it.

### `createMockUser()`
Creates a mock authenticated user for testing.

```typescript
const user = createMockUser();
// user.id === "test-user-id"
// user.email === "test@example.com"
```

## Adding New Tests

1. Create a new test file in `app/tests/routes/`
2. Import utilities from `../utils/`
3. Write tests using `describe` and `test`
4. Assert status codes and returned data

Example:

```typescript
import { describe, test, expect } from "vitest";
import { loader } from "~/routes/my-route";
import { createMockRequest } from "../utils/test-helpers";

describe("My Route", () => {
  test("returns 200", async () => {
    const request = createMockRequest("/my-route");

    // NO CONTEXT ARGUMENT. A loader here takes a request; the database is a
    // module it imports, not a binding somebody threads through.
    const response = await loader({ request, params: {} } as any);

    expect(response).toBeDefined();
  });
});
```

## CI/CD Integration

Add to your CI pipeline:

```yaml
- name: Run tests
  run: npm run test:run
```
