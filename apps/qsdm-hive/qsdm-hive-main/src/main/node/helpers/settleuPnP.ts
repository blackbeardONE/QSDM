import * as fs from 'fs';
import * as dgram from 'dgram';
import * as http from 'http';
import * as https from 'https';
import * as os from 'os';
import path from 'path';

import { getAppDataPath } from './getAppDataPath';
// Specify the log file path
const logFilePath = path.join(getAppDataPath(), 'logs', 'uPnP.log');

const SSDP_ADDRESS = '239.255.255.250';
const SSDP_PORT = 1900;
const DISCOVERY_TIMEOUT_MS = 3500;
const HTTP_TIMEOUT_MS = 8000;
const PORT_MAPPING_DESCRIPTION = 'QSDM Hive';
const PROTOCOL = 'TCP';

type GatewayService = {
  serviceType: string;
  controlURL: string;
  location: string;
  gatewayHost: string;
  localAddress: string;
};

// Custom log function to write to the log file
function log(...args: unknown[]) {
  const message = args.join(' ');
  fs.appendFile(logFilePath, `${message}\n`, (err) => {
    if (err) {
      console.error('Failed to write to log file:', err);
    }
  });
}

const xmlEscape = (value: string | number) =>
  String(value)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&apos;');

const resolveURL = (location: string, maybeRelativePath: string) =>
  new URL(maybeRelativePath, location).toString();

const requestText = (
  url: string,
  options: http.RequestOptions = {},
  body?: string
) =>
  new Promise<string>((resolve, reject) => {
    const parsed = new URL(url);
    const client = parsed.protocol === 'https:' ? https : http;
    const req = client.request(
      parsed,
      {
        ...options,
        timeout: HTTP_TIMEOUT_MS,
      },
      (res) => {
        let body = '';
        res.setEncoding('utf8');
        res.on('data', (chunk) => {
          body += chunk;
        });
        res.on('end', () => {
          if ((res.statusCode || 0) >= 200 && (res.statusCode || 0) < 300) {
            resolve(body);
            return;
          }
          reject(
            new Error(
              `HTTP ${res.statusCode || 0} from ${url}: ${body.slice(0, 300)}`
            )
          );
        });
      }
    );

    req.on('timeout', () => {
      req.destroy(new Error(`Timed out requesting ${url}`));
    });
    req.on('error', reject);

    if (body) {
      req.write(body);
    }
    req.end();
  });

const httpGetText = (url: string) => requestText(url, { method: 'GET' });

const httpPostText = (
  url: string,
  headers: Record<string, string>,
  body: string
) =>
  requestText(
    url,
    {
      method: 'POST',
      headers: {
        ...headers,
        'Content-Length': Buffer.byteLength(body).toString(),
      },
    },
    body
  );

const discoverDeviceLocations = () =>
  new Promise<string[]>((resolve) => {
    const socket = dgram.createSocket('udp4');
    const locations = new Set<string>();
    const searchTargets = [
      'urn:schemas-upnp-org:service:WANIPConnection:2',
      'urn:schemas-upnp-org:service:WANIPConnection:1',
      'urn:schemas-upnp-org:service:WANPPPConnection:1',
      'urn:schemas-upnp-org:device:InternetGatewayDevice:1',
      'upnp:rootdevice',
    ];

    const finish = () => {
      try {
        socket.close();
      } catch {
        // already closed
      }
      resolve([...locations]);
    };

    socket.on('message', (message) => {
      const response = message.toString('utf8');
      const match = response.match(/^location:\s*(.+)$/im);
      if (match?.[1]) {
        locations.add(match[1].trim());
      }
    });

    socket.on('error', (error) => {
      log('UPnP SSDP discovery socket error:', error.message);
      finish();
    });

    socket.bind(() => {
      searchTargets.forEach((st) => {
        const request = [
          'M-SEARCH * HTTP/1.1',
          `HOST: ${SSDP_ADDRESS}:${SSDP_PORT}`,
          'MAN: "ssdp:discover"',
          'MX: 2',
          `ST: ${st}`,
          '',
          '',
        ].join('\r\n');
        socket.send(Buffer.from(request), SSDP_PORT, SSDP_ADDRESS);
      });
    });

    setTimeout(finish, DISCOVERY_TIMEOUT_MS);
  });

const getTagValue = (xml: string, tagName: string) => {
  const match = xml.match(
    new RegExp(`<${tagName}\\b[^>]*>([\\s\\S]*?)<\\/${tagName}>`, 'i')
  );
  return match?.[1]?.trim() || '';
};

const findGatewayService = async () => {
  const locations = await discoverDeviceLocations();
  log('UPnP discovered device descriptions:', locations.join(', ') || 'none');

  for (const location of locations) {
    try {
      const xml = await httpGetText(location);
      const serviceMatches = xml.matchAll(
        /<service\b[^>]*>([\s\S]*?)<\/service>/gi
      );
      for (const serviceMatch of serviceMatches) {
        const serviceXml = serviceMatch[1];
        const serviceType = getTagValue(serviceXml, 'serviceType');
        const controlURL = getTagValue(serviceXml, 'controlURL');
        if (
          controlURL &&
          /urn:schemas-upnp-org:service:WAN(IP|PPP)Connection:[12]/i.test(
            serviceType
          )
        ) {
          const gatewayHost = new URL(location).hostname;
          return {
            serviceType,
            controlURL: resolveURL(location, controlURL),
            location,
            gatewayHost,
            localAddress: await getLocalAddressForGateway(gatewayHost),
          };
        }
      }
    } catch (error) {
      log('UPnP device description failed:', location, (error as Error).message);
    }
  }

  throw new Error(
    'QSDM UPnP unavailable: no Internet Gateway Device with WANIP/WANPPP service was found.'
  );
};

const getLocalAddressForGateway = (gatewayHost: string) =>
  new Promise<string>((resolve) => {
    const socket = dgram.createSocket('udp4');
    socket.once('error', () => {
      socket.close();
      resolve(getFallbackLocalIPv4Address());
    });
    socket.connect(SSDP_PORT, gatewayHost, () => {
      const address = socket.address();
      socket.close();
      resolve(
        typeof address === 'object' && address.address !== '0.0.0.0'
          ? address.address
          : getFallbackLocalIPv4Address()
      );
    });
  });

const getFallbackLocalIPv4Address = () => {
  const interfaces = os.networkInterfaces();
  for (const entries of Object.values(interfaces)) {
    for (const entry of entries || []) {
      if (entry.family === 'IPv4' && !entry.internal) {
        return entry.address;
      }
    }
  }
  return '127.0.0.1';
};

const soapRequest = (
  service: GatewayService,
  action: string,
  args: Record<string, string | number>
) => {
  const argXml = Object.entries(args)
    .map(([key, value]) => `<${key}>${xmlEscape(value)}</${key}>`)
    .join('');
  const body = [
    '<?xml version="1.0"?>',
    '<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">',
    '<s:Body>',
    `<u:${action} xmlns:u="${service.serviceType}">`,
    argXml,
    `</u:${action}>`,
    '</s:Body>',
    '</s:Envelope>',
  ].join('');

  return httpPostText(
    service.controlURL,
    {
      'Content-Type': 'text/xml; charset="utf-8"',
      SOAPAction: `"${service.serviceType}#${action}"`,
    },
    body
  );
};

const openPortCommand = async (port: number): Promise<boolean> => {
  const internalPort = port === 443 ? 30018 : 30017;

  try {
    log(`Opening UPnP port mapping external=${port} internal=${internalPort}`);
    const service = await findGatewayService();
    log(
      'UPnP gateway selected:',
      service.location,
      service.serviceType,
      service.controlURL,
      'localAddress=',
      service.localAddress
    );

    await soapRequest(service, 'AddPortMapping', {
      NewRemoteHost: '',
      NewExternalPort: port,
      NewProtocol: PROTOCOL,
      NewInternalPort: internalPort,
      NewInternalClient: service.localAddress,
      NewEnabled: 1,
      NewPortMappingDescription: PORT_MAPPING_DESCRIPTION,
      NewLeaseDuration: 0,
    });

    log(`UPnP port mapping opened external=${port} internal=${internalPort}`);
    return true;
  } catch (error) {
    throw new Error(`QSDM UPnP unavailable: ${(error as Error).message}`);
  }
};

const sleep = (ms: number) => {
  // eslint-disable-next-line no-promise-executor-return
  return new Promise((resolve) => setTimeout(resolve, ms));
};

const closePortCommand = async (port: number): Promise<boolean> => {
  try {
    log(`Closing UPnP port mapping external=${port}`);
    const service = await findGatewayService();
    await soapRequest(service, 'DeletePortMapping', {
      NewRemoteHost: '',
      NewExternalPort: port,
      NewProtocol: PROTOCOL,
    });
    log(`UPnP port mapping closed external=${port}`);
    return true;
  } catch (error) {
    log(`UPnP close failed for external=${port}:`, (error as Error).message);
    return false;
  }
};

export default { openPortCommand, closePortCommand, sleep };
