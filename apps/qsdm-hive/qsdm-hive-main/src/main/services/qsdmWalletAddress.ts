export interface QsdmWalletAddressCandidates {
  requestedAddress?: string;
  signerAddress?: string;
  configuredAddress?: string;
}

export const selectQsdmWalletAddress = ({
  requestedAddress,
  signerAddress,
  configuredAddress,
}: QsdmWalletAddressCandidates) =>
  requestedAddress?.trim() ||
  signerAddress?.trim() ||
  configuredAddress?.trim() ||
  '';
