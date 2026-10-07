import { describe, it, expect, beforeEach } from "vitest";
import { eq } from "drizzle-orm";
import * as schema from "~/db/schema";
import {
  hashPassword,
  verifyPassword,
  validatePassword,
  registerWithEmail,
  loginWithEmail,
} from "~/services/auth-email.server";
import { testDb, truncate } from "../helpers/db";

describe("Email/Password Auth Service", () => {
  let db: ReturnType<typeof testDb>;

  beforeEach(() => {
    db = testDb();
    truncate();
  });

  describe("Password hashing", () => {
    it("should hash passwords", async () => {
      const password = "test-password-123";
      const hash = await hashPassword(password);

      expect(hash).toBeDefined();
      expect(hash).not.toBe(password);
      expect(hash.startsWith("$2")).toBe(true); // bcrypt format
    });

    it("should verify correct passwords", async () => {
      const password = "test-password-123";
      const hash = await hashPassword(password);
      const valid = await verifyPassword(password, hash);

      expect(valid).toBe(true);
    });

    it("should reject incorrect passwords", async () => {
      const password = "test-password-123";
      const hash = await hashPassword(password);
      const valid = await verifyPassword("wrong-password", hash);

      expect(valid).toBe(false);
    });
  });

  describe("Password validation", () => {
    it("should reject short passwords", () => {
      const result = validatePassword("short");
      expect(result.valid).toBe(false);
      expect(result.error).toContain("at least 12 characters");
    });

    it("should accept valid passwords", () => {
      const result = validatePassword("valid-password-123");
      expect(result.valid).toBe(true);
      expect(result.error).toBeUndefined();
    });
  });

  describe("User registration", () => {
    it("should create user with hashed password", async () => {
      const user = await registerWithEmail(
        db,
        "test@example.com",
        "password-12345",
        "Test User"
      );

      expect(user.id).toBeDefined();
      expect(user.login).toBe("test");
      expect(user.name).toBe("Test User");

      // Verify user in database
      const dbUser = await db
        .select()
        .from(schema.users)
        .where(eq(schema.users.id, user.id))
        .limit(1);

      expect(dbUser[0].passwordHash).toBeDefined();
      expect(dbUser[0].email).toBe("test@example.com");
    });

    it("should reject duplicate email", async () => {
      await registerWithEmail(db, "test@example.com", "password-12345");

      await expect(
        registerWithEmail(db, "test@example.com", "another-password-123")
      ).rejects.toThrow("Email already registered");
    });

    it("should reject weak passwords", async () => {
      await expect(
        registerWithEmail(db, "test@example.com", "short")
      ).rejects.toThrow("at least 12 characters");
    });

    it("should create default workspace on registration", async () => {
      const user = await registerWithEmail(
        db,
        "test@example.com",
        "password-12345",
        "Test User"
      );

      // Verify workspace was created
      const workspaceUsers = await db
        .select()
        .from(schema.workspaceUsers)
        .where(eq(schema.workspaceUsers.userId, user.id));

      expect(workspaceUsers).toHaveLength(1);
    });
  });

  describe("User login", () => {
    beforeEach(async () => {
      // Create test user
      await registerWithEmail(db, "test@example.com", "password-12345", "Test User");
    });

    it("should login with correct credentials", async () => {
      const user = await loginWithEmail(db, "test@example.com", "password-12345");

      expect(user.id).toBeDefined();
      expect(user.login).toBe("test");
    });

    it("should reject wrong password", async () => {
      await expect(
        loginWithEmail(db, "test@example.com", "wrong-password-123")
      ).rejects.toThrow("Invalid email or password");
    });

    it("should reject non-existent email", async () => {
      await expect(
        loginWithEmail(db, "nonexistent@example.com", "password-12345")
      ).rejects.toThrow("Invalid email or password");
    });

    it("should update last login timestamp", async () => {
      const before = new Date().toISOString();
      await loginWithEmail(db, "test@example.com", "password-12345");

      const dbUser = await db
        .select()
        .from(schema.users)
        .where(eq(schema.users.email, "test@example.com"))
        .limit(1);

      expect(dbUser[0].lastLoginAt).toBeDefined();
      expect(dbUser[0].lastLoginAt! >= before).toBe(true);
    });
  });
});
