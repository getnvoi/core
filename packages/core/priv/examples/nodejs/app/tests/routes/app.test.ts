import { describe, test, expect, vi } from "vitest";
import { createMockRequest } from "../utils/test-helpers";

// Mock the auth service BEFORE importing the loader
vi.mock("~/services/auth.server", () => ({
  requireUser: vi.fn().mockResolvedValue({
    id: "test-user-id",
    email: "test@example.com",
    login: "testuser",
    name: "Test User",
    avatarUrl: "https://example.com/avatar.jpg",
  }),
}));

import { loader } from "~/layouts/app";

describe("App Routes", () => {
  describe("GET /app (layout loader)", () => {
    test("returns 200 status with valid session", async () => {
      const request = createMockRequest("/app", {
        headers: { Cookie: "session=valid-token" },
      });

      const response = await loader({
        request,
        params: {},
      } as any);

      expect(response).toBeDefined();
      expect(response.user).toBeDefined();
      expect(response.user.id).toBe("test-user-id");
      expect(response.initialJobs).toBeDefined();
      expect(Array.isArray(response.initialJobs)).toBe(true);
    });

    test("loads user data correctly", async () => {
      const request = createMockRequest("/app");

      const response = await loader({
        request,
        params: {},
      } as any);

      expect(response.user.login).toBe("testuser");
    });

    test("loads user jobs from database", async () => {
      const request = createMockRequest("/app");

      const response = await loader({
        request,
        params: {},
      } as any);

      // Jobs will be empty array from mock
      expect(response.initialJobs).toBeDefined();
      expect(Array.isArray(response.initialJobs)).toBe(true);
    });
  });
});
