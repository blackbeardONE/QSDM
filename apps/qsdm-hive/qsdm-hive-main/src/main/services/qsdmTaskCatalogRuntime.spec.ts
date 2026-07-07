import { RawTaskData } from 'models';

import {
  compareQsdmHiveVersions,
  getQsdmTaskRuntimeCompatibilityIssue,
} from './qsdmTaskCatalogRuntime';

const taskWithRuntime = (
  runtime: NonNullable<RawTaskData['manifest']>['runtime']
): RawTaskData =>
  ({
    task_id: 'shared-edge',
    manifest: {
      schema_version: 1,
      task_id: 'shared-edge',
      version: 1,
      name: 'Shared Edge',
      manager: 'a'.repeat(64),
      active: true,
      round_time: 60,
      runtime,
    },
  } as RawTaskData);

describe('QSDM task catalog runtime compatibility', () => {
  it('compares release versions numerically', () => {
    expect(compareQsdmHiveVersions('1.3.60', '1.3.9')).toBe(1);
    expect(compareQsdmHiveVersions('v1.3.60', '1.3.60')).toBe(0);
    expect(compareQsdmHiveVersions('1.3.59', '1.3.60')).toBe(-1);
  });

  it('allows the built-in generic proof capability', () => {
    expect(
      getQsdmTaskRuntimeCompatibilityIssue(
        taskWithRuntime({
          kind: 'capability',
          capability: 'generic-proof-v1',
          min_hive_version: '1.3.60',
        }),
        '1.3.60'
      )
    ).toBeUndefined();
  });

  it('blocks unknown capabilities, future Hive versions, and WASM', () => {
    expect(
      getQsdmTaskRuntimeCompatibilityIssue(
        taskWithRuntime({
          kind: 'capability',
          capability: 'unknown-v1',
        }),
        '1.3.60'
      )
    ).toMatch(/not supported/);
    expect(
      getQsdmTaskRuntimeCompatibilityIssue(
        taskWithRuntime({
          kind: 'capability',
          capability: 'generic-proof-v1',
          min_hive_version: '2.0.0',
        }),
        '1.3.60'
      )
    ).toMatch(/2.0.0 or newer/);
    expect(
      getQsdmTaskRuntimeCompatibilityIssue(
        taskWithRuntime({
          kind: 'wasm',
          module_url: 'https://qsdm.tech/task.wasm',
          module_sha256: 'a'.repeat(64),
          abi: 'qsdm-task-v1',
        }),
        '1.3.60'
      )
    ).toMatch(/WASM/);
  });
});
