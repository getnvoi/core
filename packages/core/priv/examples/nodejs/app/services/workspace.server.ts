import { eq } from "drizzle-orm";
import { type getDb, workspaces, workspaceUsers } from "~/db";

/**
 * Create a default workspace for a user
 */
export async function createDefaultWorkspace(
  db: ReturnType<typeof getDb>,
  userId: string,
  workspaceLabel: string = "My Workspace"
): Promise<string> {
  const workspaceId = crypto.randomUUID();

  // Create workspace
  await db.insert(workspaces).values({
    id: workspaceId,
    label: workspaceLabel,
    createdAt: new Date().toISOString(),
    updatedAt: new Date().toISOString(),
  });

  // Add user to workspace
  await db.insert(workspaceUsers).values({
    id: crypto.randomUUID(),
    workspaceId,
    userId,
    createdAt: new Date().toISOString(),
    updatedAt: new Date().toISOString(),
  });

  return workspaceId;
}

/**
 * Get or create default workspace for user
 */
export async function ensureUserHasWorkspace(
  db: ReturnType<typeof getDb>,
  userId: string,
  userName?: string
): Promise<string> {
  // Check if user already has a workspace
  const existingWorkspace = await db
    .select({ workspaceId: workspaceUsers.workspaceId })
    .from(workspaceUsers)
    .where(eq(workspaceUsers.userId, userId))
    .limit(1);

  if (existingWorkspace.length > 0) {
    return existingWorkspace[0].workspaceId;
  }

  // Create default workspace
  const label = userName ? `${userName}'s Workspace` : "My Workspace";
  return createDefaultWorkspace(db, userId, label);
}

/**
 * Get all workspaces for a user
 */
export async function getUserWorkspaces(
  db: ReturnType<typeof getDb>,
  userId: string
) {
  return db
    .select({
      id: workspaces.id,
      label: workspaces.label,
      createdAt: workspaces.createdAt,
      updatedAt: workspaces.updatedAt,
    })
    .from(workspaces)
    .innerJoin(workspaceUsers, eq(workspaceUsers.workspaceId, workspaces.id))
    .where(eq(workspaceUsers.userId, userId));
}
