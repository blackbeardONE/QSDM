import chalk from 'chalk';
import { execFileSync } from 'child_process';
import detectPort from 'detect-port';
import path from 'path';

const port = process.env.PORT || '1212';
const hiveRoot = path.resolve(process.cwd()).toLowerCase();
const staleProcessNames = new Set(['electron.exe', 'node.exe']);

function quotePowerShell(value) {
  return `'${String(value).replace(/'/g, "''")}'`;
}

function runPowerShell(command) {
  return execFileSync('powershell.exe', ['-NoProfile', '-Command', command], {
    encoding: 'utf8',
    stdio: ['ignore', 'pipe', 'pipe'],
  }).trim();
}

function parsePowerShellJson(output) {
  if (!output) {
    return [];
  }

  const parsed = JSON.parse(output);
  return Array.isArray(parsed) ? parsed : [parsed];
}

function getWindowsProcessesForPort() {
  const command = `
$port = ${Number(port)};
$portPids = @(Get-NetTCPConnection -LocalPort $port -State Listen -ErrorAction SilentlyContinue | Select-Object -ExpandProperty OwningProcess -Unique);
Get-CimInstance Win32_Process |
  Where-Object { ($portPids -contains $_.ProcessId) -or ($_.Name -in @('electron.exe', 'node.exe')) } |
  Select-Object ProcessId, Name, ExecutablePath, CommandLine |
  ConvertTo-Json -Depth 4
`;

  try {
    return parsePowerShellJson(runPowerShell(command));
  } catch (error) {
    console.warn(
      chalk.yellow(
        `Hive process cleanup skipped because Windows process discovery failed: ${error.message}`
      )
    );
    return [];
  }
}

function isHiveOwnedProcess(processInfo) {
  const processName = String(processInfo.Name || '').toLowerCase();
  if (!staleProcessNames.has(processName)) {
    return false;
  }

  const executablePath = String(processInfo.ExecutablePath || '').toLowerCase();
  const commandLine = String(processInfo.CommandLine || '').toLowerCase();

  return (
    executablePath.includes(`${hiveRoot}\\`) ||
    commandLine.includes(hiveRoot) ||
    commandLine.includes('qsdm-hive-main')
  );
}

function stopWindowsProcesses(processIds) {
  if (!processIds.length) {
    return;
  }

  const ids = processIds
    .map((processId) => Number(processId))
    .filter((processId) => Number.isInteger(processId) && processId > 0);

  if (!ids.length) {
    return;
  }

  const command = `
$ids = @(${ids.join(',')});
foreach ($id in $ids) {
  Stop-Process -Id $id -Force -ErrorAction SilentlyContinue
}
Start-Sleep -Milliseconds 500
`;
  runPowerShell(command);
}

function recycleHiveDevProcesses() {
  if (process.platform !== 'win32') {
    return;
  }

  const processes = getWindowsProcessesForPort();
  const staleHiveProcessIds = processes
    .filter(isHiveOwnedProcess)
    .map((processInfo) => Number(processInfo.ProcessId))
    .filter(
      (processId) =>
        Number.isInteger(processId) &&
        processId > 0 &&
        processId !== process.pid &&
        processId !== process.ppid
    );

  if (!staleHiveProcessIds.length) {
    return;
  }

  console.log(
    chalk.yellow(
      `Recycling stale QSDM Hive dev process(es): ${[
        ...new Set(staleHiveProcessIds),
      ].join(', ')}`
    )
  );
  stopWindowsProcesses([...new Set(staleHiveProcessIds)]);
}

function describePortOwner() {
  if (process.platform !== 'win32') {
    return '';
  }

  const command = `
$port = ${Number(port)};
$portPids = @(Get-NetTCPConnection -LocalPort $port -State Listen -ErrorAction SilentlyContinue | Select-Object -ExpandProperty OwningProcess -Unique);
Get-CimInstance Win32_Process |
  Where-Object { $portPids -contains $_.ProcessId } |
  Select-Object ProcessId, Name, ExecutablePath, CommandLine |
  ConvertTo-Json -Depth 4
`;

  try {
    const owners = parsePowerShellJson(runPowerShell(command));
    return owners
      .map((owner) => {
        const pathOrCommand =
          owner.ExecutablePath || owner.CommandLine || 'unknown command';
        return `${owner.Name || 'process'} pid=${owner.ProcessId} ${pathOrCommand}`;
      })
      .join('\n');
  } catch {
    return '';
  }
}

recycleHiveDevProcesses();

detectPort(port, (err, availablePort) => {
  if (err) {
    throw err;
  }

  if (port !== String(availablePort)) {
    const owner = describePortOwner();
    throw new Error(
      `${chalk.whiteBright.bgRed.bold(
        `Port "${port}" on "localhost" is already in use by a non-Hive process. Please use another port. ex: PORT=4343 npm start`
      )}${owner ? `\n${owner}` : ''}`
    );
  } else {
    process.exit(0);
  }
});
