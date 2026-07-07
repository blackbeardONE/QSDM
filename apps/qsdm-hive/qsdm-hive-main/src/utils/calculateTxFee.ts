import sdk from 'main/services/sdk';

const connection = sdk.k2Connection;
type ConnectionType = typeof connection;

export const calculateTxFee = async (
  connection: ConnectionType,
  signaturesNumber: number
) => {
  const { feeCalculator } = await connection.getRecentBlockhash();
  const fees = feeCalculator.lamportsPerSignature * signaturesNumber;
  return fees;
};

export const CELL_BASE_UNITS_PER_CELL = 1000000000;
