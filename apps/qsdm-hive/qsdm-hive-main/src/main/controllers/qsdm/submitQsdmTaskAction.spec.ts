import axios from 'axios';
import { Event } from 'electron';

import { submitQsdmTaskAction } from './submitQsdmTaskAction';

jest.mock('axios', () => ({
  post: jest.fn(),
}));

jest.mock('main/services/qsdmCanonicalChain', () => ({
  assertQsdmCanonicalChainSafety: jest.fn().mockResolvedValue({ safe: true }),
}));

const mockedAxiosPost = axios.post as jest.Mock;

describe('submitQsdmTaskAction', () => {
  beforeEach(() => {
    mockedAxiosPost.mockReset();
  });

  it('posts a signed QSDM task action envelope to QSDM Core', async () => {
    const envelope = {
      id: 'act-1',
      sender: 'sender',
      task_id: 'task-1',
      action: 'start',
      payload: '{"mode":"service"}',
      timestamp: '2026-05-28T00:00:00Z',
      signature: 'sig',
      public_key: 'pub',
      nonce: 1,
    };

    mockedAxiosPost.mockResolvedValue({
      data: {
        action_id: 'act-1',
        status: 'accepted',
        sender: 'sender',
        task_id: 'task-1',
        action: 'start',
      },
    });

    const response = await submitQsdmTaskAction({} as Event, envelope);

    expect(mockedAxiosPost).toHaveBeenCalledWith(
      'http://127.0.0.1:8080/api/v1/tasks/actions/submit-signed',
      envelope,
      { timeout: 10000 }
    );
    expect(response).toEqual({
      action_id: 'act-1',
      status: 'accepted',
      sender: 'sender',
      task_id: 'task-1',
      action: 'start',
    });
  });
});
