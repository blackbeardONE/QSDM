/**
 * @jest-environment node
 */

import axios from 'axios';

import { fetchFromIPFSOrArweave } from './fetchFromIPFSOrArweave';

jest.mock('axios', () => ({
  get: jest.fn(),
}));

jest.mock('config/qsdm', () => ({
  QSDM_TASK_RUNTIME_MODE: 'qsdm-native',
  buildQsdmTaskReadUrls: (path: string) => [
    `http://localhost:8080/api/v1/${path.replace(/^\/+/, '')}`,
  ],
}));

jest.mock('./fetchFromIPFSOrArweave', () => ({
  __esModule: true,
  fetchFromIPFSOrArweave: jest.fn(),
}));

const mockedAxiosGet = axios.get as jest.Mock;

describe('getTaskSource qsdm-native', () => {
  beforeEach(() => {
    jest.clearAllMocks();
  });

  it('generates a native task runner for a task registered in QSDM Core', async () => {
    mockedAxiosGet.mockResolvedValue({
      data: {
        tasks: [
          {
            task_id: 'qsdm-hive-local-task',
            task_audit_program: 'qsdm-hive-local-task-source',
          },
        ],
      },
    });

    const { getTaskSource } = await import('./getTaskSource');
    const result = await getTaskSource({} as Event, {
      taskAuditProgram: 'qsdm-hive-local-task-source',
    });

    expect(mockedAxiosGet).toHaveBeenCalledWith(
      'http://localhost:8080/api/v1/tasks',
      { timeout: 4000 }
    );
    expect(fetchFromIPFSOrArweave).not.toHaveBeenCalled();
    expect(result).toContain("const http = require('http');");
    expect(result).toContain('qsdm-hive-local-task');
    expect(result).toContain('/namespace-wrapper');
    expect(result).toContain('checkSubmissionAndUpdateRound');
  });
});
