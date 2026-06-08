/**
 * @jest-environment node
 */

import axios from 'axios';
import { promises as fsPromises } from 'fs';

import { fetchFromIPFSOrArweave } from './fetchFromIPFSOrArweave';

jest.mock('axios', () => ({
  get: jest.fn(),
}));

jest.mock('config/qsdm', () => ({
  QSDM_TASK_RUNTIME_MODE: 'qsdm-native',
  buildQsdmApiUrl: (path: string) =>
    `http://localhost:8080/api/v1/${path.replace(/^\/+/, '')}`,
}));

jest.mock('fs', () => ({
  promises: {
    mkdir: jest.fn(),
    access: jest.fn(),
    readFile: jest.fn(),
    writeFile: jest.fn(),
  },
}));

jest.mock('main/node/helpers/getAppDataPath', () => ({
  getAppDataPath: jest.fn(() => '/path/to/app/data'),
}));

jest.mock('./fetchFromIPFSOrArweave', () => ({
  __esModule: true,
  fetchFromIPFSOrArweave: jest.fn(),
}));

const mockedAxiosGet = axios.get as jest.Mock;

describe('getTaskMetadata qsdm-native', () => {
  beforeEach(() => {
    jest.clearAllMocks();
  });

  it('synthesizes task metadata from QSDM Core task registry', async () => {
    mockedAxiosGet.mockResolvedValue({
      data: {
        tasks: [
          {
            task_id: 'qsdm-hive-local-task',
            task_name: 'QSDM Hive Local Task',
            task_manager: 'qsdm-local-validator',
            task_metadata: 'qsdm-hive-local-task-metadata',
            task_description: 'Local QSDM-native task.',
          },
        ],
      },
    });

    const { getTaskMetadata } = await import('./getTaskMetadata');
    const result = await getTaskMetadata({} as Event, {
      metadataCID: 'qsdm-hive-local-task-metadata',
    });

    expect(mockedAxiosGet).toHaveBeenCalledWith(
      'http://localhost:8080/api/v1/tasks',
      { timeout: 10000 }
    );
    expect(fetchFromIPFSOrArweave).not.toHaveBeenCalled();
    expect(result).toEqual({
      author: 'qsdm-local-validator',
      description: 'Local QSDM-native task.',
      repositoryUrl: '',
      createdAt: 0,
      imageUrl: '',
      migrationDescription: '',
      requirementsTags: [],
      infoUrl: '',
      tags: ['qsdm-native', 'CELL'],
    });
    expect(fsPromises.writeFile).toHaveBeenCalledWith(
      '/path/to/app/data/metadata/qsdm-hive-local-task-metadata.json',
      JSON.stringify(result),
      'utf-8'
    );
  });
});
