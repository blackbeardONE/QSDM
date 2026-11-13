/**
 * Guards a walk along task migration pointers (`migrated_to`), from a task to
 * its newest version.
 *
 * The QSDM chain lets only a task's catalog manager migrate it, to another
 * registered task of the same manager, without a cycle: the producer has
 * enforced that since node release rc1110 (12 November 2026), and every block
 * does from height 1,150,000 (TaskMigrateAuthActivationHeight). Hive checks
 * the same two things as it walks, so a pointer set before that can neither
 * loop forever nor lead a user to another manager's task:
 *
 * - `enter(id)` is false for a task already visited (a cycle);
 * - `sameManager(manager)` is false once a task's manager differs from the
 *   first manager the walk saw.
 *
 * The walk has no step limit: every step visits a task it has not seen, and the
 * catalog is finite.
 */
export class TaskMigrationWalk {
  private readonly seen = new Set<string>();

  private manager: string | undefined;

  /**
   * @param startId the task the walk starts from, if it is not entered by the
   *   walk itself (so a pointer back to it counts as a cycle)
   * @param manager that task's manager, if already known
   */
  constructor(startId?: string, manager?: string) {
    if (startId) {
      this.seen.add(startId);
    }
    this.manager = manager;
  }

  /** Records a step to taskId; false when there is no id or it was visited. */
  enter(taskId: string | null | undefined): boolean {
    if (!taskId || this.seen.has(taskId)) {
      return false;
    }
    this.seen.add(taskId);
    return true;
  }

  /** False when manager differs from the first manager this walk saw. */
  sameManager(manager: unknown): boolean {
    const value = String(manager ?? '');
    if (this.manager === undefined) {
      this.manager = value;
      return true;
    }
    return value === this.manager;
  }
}
