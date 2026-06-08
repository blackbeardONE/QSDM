import fs from 'fs';
import path from 'path';

import { getAppDataPath } from './node/helpers/getAppDataPath';

type StoreData = Record<string, unknown>;

class FileBackedDb {
  private readonly filePath: string;

  private data: StoreData | null = null;

  constructor(filePath: string) {
    this.filePath = filePath;
  }

  private load(): StoreData {
    if (this.data) return this.data;

    try {
      if (!fs.existsSync(this.filePath)) {
        this.data = {};
        return this.data;
      }

      const raw = fs.readFileSync(this.filePath, 'utf-8').trim();
      this.data = raw ? (JSON.parse(raw) as StoreData) : {};
      return this.data;
    } catch (error) {
      console.error('Failed to load QSDM Hive DB; starting with empty store', {
        filePath: this.filePath,
        error,
      });
      this.data = {};
      return this.data;
    }
  }

  private persist() {
    const dir = path.dirname(this.filePath);
    fs.mkdirSync(dir, { recursive: true });

    const tmpPath = `${this.filePath}.tmp`;
    fs.writeFileSync(tmpPath, JSON.stringify(this.load(), null, 2), 'utf-8');
    fs.renameSync(tmpPath, this.filePath);
  }

  async get(key: string) {
    return this.load()[key];
  }

  async put(key: string, value: unknown) {
    this.load()[key] = value;
    this.persist();
    return value;
  }

  async del(key: string) {
    delete this.load()[key];
    this.persist();
  }

  compactDatafile() {
    this.persist();
  }
}

export default new FileBackedDb(`${getAppDataPath()}/QSDMHiveDB.db`);
