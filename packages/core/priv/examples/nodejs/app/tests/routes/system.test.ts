import { describe, test, expect } from "vitest";
import SystemDashboard from "~/routes/system";

describe("System Routes", () => {
  describe("GET /system", () => {
    test("returns component successfully", () => {
      const component = SystemDashboard();

      expect(component).toBeDefined();
      expect(component.type).toBeDefined();
    });
  });
});
