import fs from 'fs';

import rimraf from 'rimraf';

import webpackPaths from '../configs/webpack.paths';

const foldersToRemove = [
  webpackPaths.distPath,
  webpackPaths.buildPath,
  webpackPaths.dllPath,
];

const retryableWindowsErrors = new Set(['EBUSY', 'ENOTEMPTY', 'EPERM']);
const waitBuffer = new Int32Array(new SharedArrayBuffer(4));

const removeBuildFolder = (folder) => {
  for (let attempt = 0; attempt < 30; attempt += 1) {
    try {
      rimraf.sync(folder);
      return;
    } catch (error) {
      if (!retryableWindowsErrors.has(error.code) || attempt === 29) {
        throw error;
      }
      Atomics.wait(waitBuffer, 0, 0, Math.min((attempt + 1) * 100, 1000));
    }
  }
};

foldersToRemove.forEach((folder) => {
  if (fs.existsSync(folder)) removeBuildFolder(folder);
});
