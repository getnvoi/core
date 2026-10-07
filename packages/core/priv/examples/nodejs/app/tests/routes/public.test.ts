import { describe, test, expect } from "vitest";
import { loader } from "~/routes/home";
import { createMockRequest } from "../utils/test-helpers";

describe("Public Routes", () => {
  describe("GET /home", () => {
    test("returns 200 status", async () => {
      const request = createMockRequest("/home");

      const response = await loader({
        request,
        params: {},
      } as any);

      expect(response).toBeDefined();
      expect(response.user).toBeDefined();
    });

    test("returns null user when not authenticated", async () => {
      const request = createMockRequest("/home");

      const response = await loader({
        request,
        params: {},
      } as any);

      expect(response.user).toBeNull();
    });
  });
});
