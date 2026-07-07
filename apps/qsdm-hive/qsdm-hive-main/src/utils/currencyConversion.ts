export const CELL_BASE_UNITS_PER_CELL = 1000000000;

const toMax3Decimals = (value: number) =>
  +parseFloat(String(+value)).toFixed(3);

export const getCellFromBaseUnits = (baseUnits: number) =>
  toMax3Decimals(baseUnits / CELL_BASE_UNITS_PER_CELL);

export const getDenominationFromMainUnit = (
  value: number,
  decimals: number
) => {
  return value * 10 ** decimals;
};

export const getMainUnitFromDenomination = (
  value: number,
  decimals: number
) => {
  return value / 10 ** decimals;
};

export const getBaseUnitsFromCell = (cell: number) =>
  cell * CELL_BASE_UNITS_PER_CELL;

export const getFullCellFromBaseUnits = (baseUnits: number) =>
  baseUnits / CELL_BASE_UNITS_PER_CELL;
