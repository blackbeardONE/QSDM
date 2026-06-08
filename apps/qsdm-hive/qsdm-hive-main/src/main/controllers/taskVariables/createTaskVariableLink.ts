import { BrowserWindow } from 'electron';

import { RendererEndpoints } from 'config/endpoints';
import { Request, Response } from 'express';
import { namespaceInstance } from 'main/node/helpers/Namespace';
import { TaskVariableData, TaskVariables } from 'models';
// eslint-disable-next-line @cspell/spellchecker
import { v4 as uuidv4 } from 'uuid';

import { PersistentStoreKeys } from '../types';

import { getStoredTaskVariables } from './getStoredTaskVariables';

const notifyTaskVariablesUpdated = () => {
  try {
    const appWindow = BrowserWindow.getAllWindows()[0];
    appWindow?.webContents.send(RendererEndpoints.TASK_VARIABLES_UPDATED);
  } catch (e) {
    console.error(e);
  }
};

export const createTaskVariable = async (req: Request, res: Response) => {
  try {
    const taskVariable: TaskVariableData = req.body;

    // Validate required fields
    if (!taskVariable.label || !taskVariable.value) {
      return res.status(400).json({
        error: 'Missing required fields: label and value are required',
      });
    }

    // Get existing task variables
    const existingVariables = await getStoredTaskVariables();

    // Check for duplicate labels
    const isDuplicateLabel = Object.values(existingVariables).some(
      (variable) => variable.label === taskVariable.label
    );
    if (isDuplicateLabel) {
      return res.status(400).json({
        error: `Task variable with label "${taskVariable.label}" already exists`,
      });
    }

    // Generate new ID and add to existing variables
    const newId = uuidv4();
    const newTaskVariables: TaskVariables = {
      ...existingVariables,
      [newId]: taskVariable,
    };

    // Store updated variables
    await namespaceInstance.storeSet(
      PersistentStoreKeys.TaskVariables,
      JSON.stringify(newTaskVariables)
    );

    notifyTaskVariablesUpdated();

    res.json({
      success: true,
      id: newId,
      taskVariable,
    });
  } catch (error) {
    console.error('Error creating task variable:', error);
    res.status(500).json({
      error: 'Failed to create task variable',
    });
  }
};

export const upsertTaskVariable = async (req: Request, res: Response) => {
  try {
    const taskVariable: TaskVariableData = req.body;

    if (!taskVariable.label || !taskVariable.value) {
      return res.status(400).json({
        success: false,
        error: 'Missing required fields: label and value are required',
      });
    }

    const label = String(taskVariable.label).trim();
    const value = String(taskVariable.value);

    if (!label || !value) {
      return res.status(400).json({
        success: false,
        error: 'Missing required fields: label and value are required',
      });
    }

    const existingVariables = await getStoredTaskVariables();
    const existingEntry = Object.entries(existingVariables).find(
      ([, variable]) => variable.label === label
    );

    const id = existingEntry?.[0] || uuidv4();
    const action = existingEntry ? 'updated' : 'created';
    const newTaskVariables: TaskVariables = {
      ...existingVariables,
      [id]: { label, value },
    };

    await namespaceInstance.storeSet(
      PersistentStoreKeys.TaskVariables,
      JSON.stringify(newTaskVariables)
    );

    notifyTaskVariablesUpdated();

    return res.json({
      success: true,
      id,
      action,
      taskVariable: { label, value },
    });
  } catch (error) {
    console.error('Error upserting task variable:', error);
    return res.status(500).json({
      success: false,
      error: 'Failed to save task variable',
    });
  }
};
