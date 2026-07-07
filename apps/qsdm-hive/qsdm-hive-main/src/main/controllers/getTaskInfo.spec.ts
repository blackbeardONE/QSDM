import qsdmHiveTasks from 'main/services/qsdmHiveTasks';

import { parseRawK2TaskData } from '../node/helpers/parseRawK2TaskData';

import { getTaskInfo } from './getTaskInfo';

jest.mock('main/services/qsdmHiveTasks', () => ({
  getStartedTasks: jest.fn(),
  getTaskState: jest.fn(),
}));
jest.mock('../node/helpers/parseRawK2TaskData', () => ({
  parseRawK2TaskData: jest.fn(),
}));

describe('getTaskInfo', () => {
  const mockEvent = {} as any; // Mock Event object as needed
  const mockPayload = { taskAccountPubKey: 'testPubKey' };
  const mockContext = 'testContext';

  beforeEach(() => {
    jest.clearAllMocks();
  });

  it('should successfully get task info', async () => {
    const mockPartialRawTaskData = { task_name: 'Test Task' };
    (qsdmHiveTasks.getStartedTasks as jest.Mock).mockResolvedValue([
      { task_id: mockPayload.taskAccountPubKey, ...mockPartialRawTaskData },
    ]);
    const mockParsedData = { taskName: 'Test Task', taskId: 'testPubKey' };
    (parseRawK2TaskData as jest.Mock).mockReturnValue(mockParsedData);

    const result = await getTaskInfo(mockEvent, mockPayload, mockContext);

    expect(parseRawK2TaskData).toHaveBeenCalledWith({
      rawTaskData: {
        ...mockPartialRawTaskData,
        task_id: mockPayload.taskAccountPubKey,
      },
    });
    expect(result).toEqual(mockParsedData);
  });

  it("should throw a detailed error if there's no task state for the account provided", async () => {
    (qsdmHiveTasks.getStartedTasks as jest.Mock).mockResolvedValue([
      { task_id: 'someOtherTaskWeDoNotCareAbout' },
    ]);

    await expect(
      getTaskInfo(mockEvent, mockPayload, mockContext)
    ).rejects.toThrow(
      '{"detailed":"Error during Task parsing in context of testContext: Error: {\\"detailed\\":\\"Task not found\\",\\"type\\":\\"TASK_NOT_FOUND\\"}","type":"TASK_NOT_FOUND"}'
    );
  });
});
