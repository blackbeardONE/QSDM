import { Request, Response } from 'express';
import { namespaceInstance } from 'main/node/helpers/Namespace';

import { PersistentStoreKeys } from '../types';

import {
  createTaskVariable,
  upsertTaskVariable,
} from './createTaskVariableLink';
import { getStoredTaskVariables } from './getStoredTaskVariables';

// Mock UUID to have consistent IDs in tests
jest.mock('uuid', () => ({
  v4: () => 'mocked-uuid',
}));

jest.mock('main/node/helpers/Namespace', () => ({
  namespaceInstance: {
    storeSet: jest.fn(),
  },
}));

jest.mock('./getStoredTaskVariables', () => ({
  getStoredTaskVariables: jest.fn(),
}));

const getStoredTaskVariablesMock = getStoredTaskVariables as jest.Mock;
const storeSetMock = namespaceInstance.storeSet as jest.Mock;

describe('createTaskVariable', () => {
  let mockRequest: Partial<Request>;
  let mockResponse: Partial<Response>;
  let jsonMock: jest.Mock;
  let statusMock: jest.Mock;

  beforeEach(() => {
    jest.clearAllMocks();
    storeSetMock.mockResolvedValue(undefined);
    jsonMock = jest.fn();
    statusMock = jest.fn().mockReturnValue({ json: jsonMock });
    mockResponse = {
      json: jsonMock,
      status: statusMock,
    };
  });

  it('creates a new task variable successfully', async () => {
    getStoredTaskVariablesMock.mockResolvedValue({});
    mockRequest = {
      body: {
        label: 'New Variable',
        value: 'test value',
      },
    };

    await createTaskVariable(mockRequest as Request, mockResponse as Response);

    expect(storeSetMock).toHaveBeenCalledWith(
      PersistentStoreKeys.TaskVariables,
      JSON.stringify({
        'mocked-uuid': {
          label: 'New Variable',
          value: 'test value',
        },
      })
    );
    expect(jsonMock).toHaveBeenCalledWith({
      success: true,
      id: 'mocked-uuid',
      taskVariable: {
        label: 'New Variable',
        value: 'test value',
      },
    });
  });

  it('returns 400 if label is missing', async () => {
    mockRequest = {
      body: {
        value: 'test value',
      },
    };

    await createTaskVariable(mockRequest as Request, mockResponse as Response);

    expect(statusMock).toHaveBeenCalledWith(400);
    expect(jsonMock).toHaveBeenCalledWith({
      error: 'Missing required fields: label and value are required',
    });
  });

  it('returns 400 if value is missing', async () => {
    mockRequest = {
      body: {
        label: 'New Variable',
      },
    };

    await createTaskVariable(mockRequest as Request, mockResponse as Response);

    expect(statusMock).toHaveBeenCalledWith(400);
    expect(jsonMock).toHaveBeenCalledWith({
      error: 'Missing required fields: label and value are required',
    });
  });

  it('returns 400 if label already exists', async () => {
    getStoredTaskVariablesMock.mockResolvedValue({
      'existing-id': {
        label: 'New Variable',
        value: 'existing value',
      },
    });

    mockRequest = {
      body: {
        label: 'New Variable',
        value: 'test value',
      },
    };

    await createTaskVariable(mockRequest as Request, mockResponse as Response);

    expect(statusMock).toHaveBeenCalledWith(400);
    expect(jsonMock).toHaveBeenCalledWith({
      error: 'Task variable with label "New Variable" already exists',
    });
  });

  it('returns 500 if storage operation fails', async () => {
    getStoredTaskVariablesMock.mockResolvedValue({});
    storeSetMock.mockRejectedValue(new Error('Storage error'));

    mockRequest = {
      body: {
        label: 'New Variable',
        value: 'test value',
      },
    };

    await createTaskVariable(mockRequest as Request, mockResponse as Response);

    expect(statusMock).toHaveBeenCalledWith(500);
    expect(jsonMock).toHaveBeenCalledWith({
      error: 'Failed to create task variable',
    });
  });
});

describe('upsertTaskVariable', () => {
  let mockRequest: Partial<Request>;
  let mockResponse: Partial<Response>;
  let jsonMock: jest.Mock;
  let statusMock: jest.Mock;

  beforeEach(() => {
    jest.clearAllMocks();
    storeSetMock.mockResolvedValue(undefined);
    jsonMock = jest.fn();
    statusMock = jest.fn().mockReturnValue({ json: jsonMock });
    mockResponse = {
      json: jsonMock,
      status: statusMock,
    };
  });

  it('creates a task variable when the label is new', async () => {
    getStoredTaskVariablesMock.mockResolvedValue({});
    mockRequest = {
      body: {
        label: 'ANTHROPIC_API_KEY',
        value: 'secret',
      },
    };

    await upsertTaskVariable(mockRequest as Request, mockResponse as Response);

    expect(storeSetMock).toHaveBeenCalledWith(
      PersistentStoreKeys.TaskVariables,
      JSON.stringify({
        'mocked-uuid': {
          label: 'ANTHROPIC_API_KEY',
          value: 'secret',
        },
      })
    );
    expect(jsonMock).toHaveBeenCalledWith({
      success: true,
      id: 'mocked-uuid',
      action: 'created',
      taskVariable: {
        label: 'ANTHROPIC_API_KEY',
        value: 'secret',
      },
    });
  });

  it('updates the existing task variable when the label already exists', async () => {
    getStoredTaskVariablesMock.mockResolvedValue({
      'existing-id': {
        label: 'ANTHROPIC_API_KEY',
        value: 'old secret',
      },
    });
    mockRequest = {
      body: {
        label: 'ANTHROPIC_API_KEY',
        value: 'new secret',
      },
    };

    await upsertTaskVariable(mockRequest as Request, mockResponse as Response);

    expect(storeSetMock).toHaveBeenCalledWith(
      PersistentStoreKeys.TaskVariables,
      JSON.stringify({
        'existing-id': {
          label: 'ANTHROPIC_API_KEY',
          value: 'new secret',
        },
      })
    );
    expect(jsonMock).toHaveBeenCalledWith({
      success: true,
      id: 'existing-id',
      action: 'updated',
      taskVariable: {
        label: 'ANTHROPIC_API_KEY',
        value: 'new secret',
      },
    });
  });
});
