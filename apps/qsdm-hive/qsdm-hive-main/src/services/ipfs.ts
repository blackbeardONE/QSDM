import config from 'config';
import { fetchWithTimeout } from 'main/node/helpers';
import { ErrorType } from 'models';
import { throwDetailedError } from 'utils';

export async function retrieveFromIPFS(
  cid: string,
  fileName: string
): Promise<string> {
  try {
    return await retrieveThroughHttpGateway(cid, fileName);
  } catch (error) {
    return throwDetailedError({
      detailed: error instanceof Error ? error.message : String(error),
      type: ErrorType.GENERIC,
    });
  }
}

async function retrieveThroughHttpGateway(
  cid: string,
  fileName = ''
): Promise<string> {
  console.log('use IPFS HTTP gateway');

  const listOfIpfsGatewaysUrls = [
    `https://${cid}.ipfs.w3s.link/${fileName}`,
    `https://${cid}.ipfs.dweb.link/${fileName}`,
    `https://nftstorage.link/ipfs/${cid}/${fileName}`,
    `https://gateway.pinata.cloud/ipfs/${cid}/${fileName}`,
    `${config.node.IPFS_GATEWAY_URL}/${cid}/${fileName}`,
    `https://ipfs.io/ipfs/${cid}/${fileName}`,
  ];

  for (const url of listOfIpfsGatewaysUrls) {
    try {
      const response = await fetchWithTimeout(url);
      if (!response.ok) {
        console.log(
          `Gateway returned HTTP ${response.status} at ${url}, trying next if available.`
        );
      } else {
        const fileContent = await response.text();
        const couldNotFetchActualFileContent = fileContent
          .trimStart()
          .startsWith('<');

        if (!couldNotFetchActualFileContent) {
          return fileContent;
        }

        console.log(`Gateway failed at ${url}, trying next if available.`);
      }
    } catch (error) {
      console.error(`Error fetching from ${url}:`, error);
    }
  }

  throw Error(`Failed to get ${cid} from IPFS`);
}
