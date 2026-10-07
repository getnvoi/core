import bcrypt from "bcryptjs";
import { eq } from "drizzle-orm";
import { type getDb, users, systemUsers } from "~/db";
import type { SessionUser } from "~/sessions.server";
import { ensureUserHasWorkspace } from "./workspace.server";

const BCRYPT_ROUNDS = 12; // bcrypt's cost factor
const MIN_PASSWORD_LENGTH = 12; // The shortest password accepted

/**
 * Hash a password using bcrypt
 */
export async function hashPassword(password: string): Promise<string> {
  return bcrypt.hash(password, BCRYPT_ROUNDS);
}

/**
 * Verify a password against a hash
 */
export async function verifyPassword(
  password: string,
  hash: string
): Promise<boolean> {
  return bcrypt.compare(password, hash);
}

/**
 * Validate password strength
 */
export function validatePassword(password: string): {
  valid: boolean;
  error?: string;
} {
  if (password.length < MIN_PASSWORD_LENGTH) {
    return {
      valid: false,
      error: `Password must be at least ${MIN_PASSWORD_LENGTH} characters`,
    };
  }
  return { valid: true };
}

/**
 * Register a new user with email and password
 */
export async function registerWithEmail(
  db: ReturnType<typeof getDb>,
  email: string,
  password: string,
  name?: string
): Promise<SessionUser> {
  // Validate password
  const validation = validatePassword(password);
  if (!validation.valid) {
    throw new Error(validation.error);
  }

  // Check if email already exists
  const existing = await db
    .select()
    .from(users)
    .where(eq(users.email, email))
    .limit(1);

  if (existing.length > 0) {
    throw new Error("Email already registered");
  }

  // Hash password
  const passwordHash = await hashPassword(password);

  // Create user
  const userId = crypto.randomUUID();
  await db.insert(users).values({
    id: userId,
    email,
    passwordHash,
    name,
    createdAt: new Date().toISOString(),
  });

  // Create default workspace
  await ensureUserHasWorkspace(db, userId, name || email.split("@")[0]);

  return {
    id: userId,
    login: email.split("@")[0],
    name,
    avatarUrl: undefined,
  };
}

/**
 * Login with email and password
 */
export async function loginWithEmail(
  db: ReturnType<typeof getDb>,
  email: string,
  password: string
): Promise<SessionUser> {
  // Find user by email
  const userResults = await db
    .select()
    .from(users)
    .where(eq(users.email, email))
    .limit(1);

  const user = userResults[0];

  if (!user || !user.passwordHash) {
    throw new Error("Invalid email or password");
  }

  // Verify password
  const valid = await verifyPassword(password, user.passwordHash);
  if (!valid) {
    throw new Error("Invalid email or password");
  }

  // Update last login
  await db
    .update(users)
    .set({ lastLoginAt: new Date().toISOString() })
    .where(eq(users.id, user.id));

  return {
    id: user.id,
    login: user.login || email.split("@")[0],
    name: user.name,
    avatarUrl: user.avatarUrl,
  };
}

/**
 * Register a system user (admin/internal)
 */
export async function registerSystemUser(
  db: ReturnType<typeof getDb>,
  email: string,
  password: string,
  name?: string
): Promise<string> {
  // Validate password
  const validation = validatePassword(password);
  if (!validation.valid) {
    throw new Error(validation.error);
  }

  // Check if email already exists
  const existing = await db
    .select()
    .from(systemUsers)
    .where(eq(systemUsers.email, email))
    .limit(1);

  if (existing.length > 0) {
    throw new Error("Email already registered");
  }

  // Hash password
  const passwordHash = await hashPassword(password);

  // Create system user
  const userId = crypto.randomUUID();
  await db.insert(systemUsers).values({
    id: userId,
    email,
    passwordHash,
    name,
    createdAt: new Date().toISOString(),
    updatedAt: new Date().toISOString(),
  });

  return userId;
}

/**
 * Login system user
 */
export async function loginSystemUser(
  db: ReturnType<typeof getDb>,
  email: string,
  password: string
): Promise<{ id: string; email: string; name: string | null }> {
  // Find system user by email
  const userResults = await db
    .select()
    .from(systemUsers)
    .where(eq(systemUsers.email, email))
    .limit(1);

  const user = userResults[0];

  if (!user) {
    throw new Error("Invalid email or password");
  }

  // Verify password
  const valid = await verifyPassword(password, user.passwordHash);
  if (!valid) {
    throw new Error("Invalid email or password");
  }

  // Update last login
  await db
    .update(systemUsers)
    .set({ updatedAt: new Date().toISOString() })
    .where(eq(systemUsers.id, user.id));

  return {
    id: user.id,
    email: user.email,
    name: user.name,
  };
}
