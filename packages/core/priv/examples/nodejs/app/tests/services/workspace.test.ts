import { describe, it, expect, beforeEach } from "vitest";
import { eq } from "drizzle-orm";
import * as schema from "~/db/schema";
import {
  createDefaultWorkspace,
  ensureUserHasWorkspace,
  getUserWorkspaces,
} from "~/services/workspace.server";
import { testDb, truncate } from "../helpers/db";

describe("Workspace Service", () => {
  let db: ReturnType<typeof testDb>;
  let testUserId: string;

  beforeEach(async () => {
    db = testDb();
    truncate();

    // Create test user
    testUserId = crypto.randomUUID();
    await db.insert(schema.users).values({
      id: testUserId,
      email: "test@example.com",
      login: "testuser",
      name: "Test User",
      createdAt: new Date().toISOString(),
    });
  });


  describe("createDefaultWorkspace", () => {
    it("should create a workspace and add user to it", async () => {
      const workspaceId = await createDefaultWorkspace(
        db,
        testUserId,
        "Test Workspace"
      );

      expect(workspaceId).toBeDefined();
      expect(typeof workspaceId).toBe("string");

      // Verify workspace was created
      const workspaces = await db
        .select()
        .from(schema.workspaces)
        .where(eq(schema.workspaces.id, workspaceId));

      expect(workspaces).toHaveLength(1);
      expect(workspaces[0].label).toBe("Test Workspace");

      // Verify user was added to workspace
      const workspaceUsers = await db
        .select()
        .from(schema.workspaceUsers)
        .where(eq(schema.workspaceUsers.workspaceId, workspaceId));

      expect(workspaceUsers).toHaveLength(1);
      expect(workspaceUsers[0].userId).toBe(testUserId);
    });
  });

  describe("ensureUserHasWorkspace", () => {
    it("should create workspace if user has none", async () => {
      const workspaceId = await ensureUserHasWorkspace(
        db,
        testUserId,
        "Test User"
      );

      expect(workspaceId).toBeDefined();

      const workspaces = await getUserWorkspaces(db, testUserId);
      expect(workspaces).toHaveLength(1);
      expect(workspaces[0].label).toBe("Test User's Workspace");
    });

    it("should return existing workspace if user already has one", async () => {
      // Create first workspace
      const workspaceId1 = await createDefaultWorkspace(
        db,
        testUserId,
        "First Workspace"
      );

      // Ensure should return existing workspace
      const workspaceId2 = await ensureUserHasWorkspace(db, testUserId);

      expect(workspaceId2).toBe(workspaceId1);

      // Should still only have one workspace
      const workspaces = await getUserWorkspaces(db, testUserId);
      expect(workspaces).toHaveLength(1);
    });
  });

  describe("getUserWorkspaces", () => {
    it("should return empty array if user has no workspaces", async () => {
      const workspaces = await getUserWorkspaces(db, testUserId);
      expect(workspaces).toHaveLength(0);
    });

    it("should return all user workspaces", async () => {
      // Create two workspaces
      await createDefaultWorkspace(db, testUserId, "Workspace 1");
      await createDefaultWorkspace(db, testUserId, "Workspace 2");

      const workspaces = await getUserWorkspaces(db, testUserId);
      expect(workspaces).toHaveLength(2);
      expect(workspaces.map((w) => w.label)).toContain("Workspace 1");
      expect(workspaces.map((w) => w.label)).toContain("Workspace 2");
    });
  });
});
