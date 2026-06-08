import axios from 'axios';
import { Event } from 'electron';

import { submitQsdmSignedTransaction } from './submitQsdmSignedTransaction';

jest.mock('axios', () => ({
  post: jest.fn(),
}));

const mockedAxiosPost = axios.post as jest.Mock;

describe('submitQsdmSignedTransaction', () => {
  beforeEach(() => {
    mockedAxiosPost.mockReset();
  });

  it('posts a signed self-custody envelope to QSDM Core', async () => {
    const envelope = {
      id: 'tx-1',
      sender: 'sender',
      recipient: 'recipient',
      amount: 1,
      fee: 0,
      geotag: 'US',
      parent_cells: ['a', 'b'],
      timestamp: '2026-05-27T00:00:00Z',
      signature: 'sig',
      public_key: 'pub',
      nonce: 1,
    };

    mockedAxiosPost.mockResolvedValue({
      data: {
        transaction_id: 'tx-1',
        status: 'accepted',
        broadcast: 'local-only',
      },
    });

    const response = await submitQsdmSignedTransaction({} as Event, envelope);

    expect(mockedAxiosPost).toHaveBeenCalledWith(
      'http://127.0.0.1:8080/api/v1/wallet/submit-signed',
      envelope,
      { timeout: 10000 }
    );
    expect(response).toEqual({
      transaction_id: 'tx-1',
      status: 'accepted',
      broadcast: 'local-only',
    });
  });
});
