/* eslint-disable consistent-return */
import { randomUUID } from 'crypto';

import { CronJob } from 'cron';
import {
  parse,
  isBefore,
  addDays,
  areIntervalsOverlapping,
  format,
} from 'date-fns';

import { SystemDbKeys } from 'config/systemDbKeys';
import { clone, isEqual, isNil, isNumber } from 'lodash';
import {
  ErrorType,
  Schedule,
  ScheduleMetadata,
  ScheduleMetadataUpdateType,
  TimeFormat,
} from 'models';
import { throwDetailedError } from 'utils';

import getUserConfig from '../controllers/getUserConfig';
import { NodeNamespace } from '../NodeNamespace';

import { getCronTime } from './utils/getCronTime';

export class TaskSchedulerService {
  private namespace: NodeNamespace;

  private scheduleStartAction: () => Promise<void>;

  private scheduleEndAction: () => Promise<void>;

  public schedules: Map<string, Schedule> = new Map();

  constructor(
    namespace: NodeNamespace,
    scheduleStartAction: () => Promise<void>,
    scheduleEndAction: () => Promise<void>
  ) {
    console.log('CREATING SCHEDULER INSTANCE');
    this.namespace = namespace;
    this.scheduleStartAction = scheduleStartAction;
    this.scheduleEndAction = scheduleEndAction;

    this.loadAndStartSchedules();
  }

  private async loadAndStartSchedules() {
    const schedules = await this.getSchedulesFromDb();
    const userConfig = await getUserConfig();
    const canRunSchedules = isNumber(userConfig.stayAwake);

    schedules.forEach((schedule) => {
      console.log(
        'Loading schedule',
        schedule.id,
        schedule.startTime,
        schedule.days
      );
      const fullSchedule = {
        ...schedule,
        startJob: this.createCronJob(
          this.scheduleStartAction,
          schedule.startTime,
          schedule.days,
          schedule.id
        ),
        stopJob: schedule.stopTime
          ? this.createCronJob(
              this.scheduleEndAction,
              schedule.stopTime,
              schedule.days,
              schedule.id
            )
          : null,
      };

      this.schedules.set(schedule.id, fullSchedule);

      if (canRunSchedules && schedule.isEnabled) {
        fullSchedule.startJob?.start();
        fullSchedule.stopJob?.start();
      }
    });
  }

  async getSchedulesFromDb() {
    const schedulesJson = await this.namespace.storeGet(SystemDbKeys.Schedules);
    if (!schedulesJson) {
      return [];
    }
    try {
      return (JSON.parse(schedulesJson) as ScheduleMetadata[]) || [];
    } catch (err) {
      console.error('GET SCHEDULES', err);
      return [];
    }
  }

  async getSchedule(id: string) {
    const schedules = await this.getSchedulesFromDb();
    return schedules.find((schedule) => schedule.id === id);
  }

  private async saveSchedulesToDb() {
    const schedules: ScheduleMetadata[] = Array.from(
      this.schedules.values()
    ).map(
      // eslint-disable-next-line @typescript-eslint/no-unused-vars
      ({ startJob, stopJob, ...rest }) => rest
    );
    await this.namespace.storeSet(
      SystemDbKeys.Schedules,
      JSON.stringify(schedules)
    );
  }

  // eslint-disable-next-line class-methods-use-this
  public createCronJob(
    action: () => Promise<void>,
    actionTime: TimeFormat,
    days: number[],
    id: string
  ): CronJob {
    const cronTime = getCronTime(actionTime, days);

    console.log('createCronJob', cronTime, actionTime, days);

    return new CronJob(cronTime, () => {
      console.log(
        `Action Time for Schedule Id ${id}, action time ${actionTime}`
      );
      return action();
    });
  }

  public async setTaskSchedule({
    id,
    startTime,
    stopTime,
    days,
    isEnabled,
  }: ScheduleMetadata) {
    const scheduleId = id || randomUUID();

    const schedule: Schedule = {
      id: scheduleId,
      startTime,
      stopTime,
      days,
      isEnabled,
      startJob: this.createCronJob(
        this.scheduleStartAction,
        startTime,
        days,
        scheduleId
      ),
      stopJob: stopTime
        ? this.createCronJob(this.scheduleEndAction, stopTime, days, scheduleId)
        : null,
    };

    this.schedules.set(scheduleId, schedule);

    if (isEnabled && isNumber((await getUserConfig()).stayAwake)) {
      schedule.startJob?.start();
      schedule.stopJob?.start();
    }

    await this.saveSchedulesToDb();
  }

  checkIsScheduleInConflict(
    checkedScheduleId: string,
    checkedStartTime: Date,
    checkedStopTime: Date | null,
    checkedDays: number[]
  ): boolean {
    if (!checkedStopTime) {
      return false;
    }

    const comparisonDate = new Date(2023, 0, 1);

    const checkedInterval = {
      start: parse(
        format(checkedStartTime, 'HH:mm:ss'),
        'HH:mm:ss',
        comparisonDate
      ),
      end: parse(
        format(checkedStopTime, 'HH:mm:ss'),
        'HH:mm:ss',
        comparisonDate
      ),
    };

    return Array.from(this.schedules.values()).some((schedule) => {
      if (schedule.id === checkedScheduleId) {
        return false;
      }

      const startParsed = parse(schedule.startTime, 'HH:mm:ss', comparisonDate);
      let stopParsed = schedule.stopTime
        ? parse(schedule.stopTime, 'HH:mm:ss', comparisonDate)
        : null;

      if (!stopParsed) {
        return false;
      }

      // If stopParsed is before startParsed, add 1 day to stopParsed
      if (isBefore(stopParsed, startParsed)) {
        stopParsed = addDays(stopParsed, 1);
      }

      const iteratedInterval = {
        start: startParsed,
        end: stopParsed,
      };

      // Ensure that the checked interval end time is also adjusted if it is before the start time
      if (isBefore(checkedInterval.end, checkedInterval.start)) {
        checkedInterval.end = addDays(checkedInterval.end, 1);
      }

      if (areIntervalsOverlapping(checkedInterval, iteratedInterval)) {
        return checkedDays.some((day) => schedule.days.includes(day));
      }
      return false;
    });
  }

  async updateTaskSchedule(
    scheduleData: ScheduleMetadataUpdateType
  ): Promise<void> {
    const {
      id,
      startTime: newStartTime,
      stopTime: newStopTime,
      days: newDays,
      isEnabled: newIsEnabled,
    } = scheduleData;

    const schedule = clone(this.schedules.get(id));

    if (schedule) {
      const hasNewStartTime = Object.prototype.hasOwnProperty.call(
        scheduleData,
        'startTime'
      );
      const hasNewStopTime = Object.prototype.hasOwnProperty.call(
        scheduleData,
        'stopTime'
      );
      const hasNewDays = Object.prototype.hasOwnProperty.call(
        scheduleData,
        'days'
      );

      const effectiveStartTime = newStartTime || schedule.startTime;
      const effectiveStopTime = hasNewStopTime
        ? newStopTime || null
        : schedule.stopTime;
      const effectiveDays = hasNewDays && newDays ? newDays : schedule.days;
      const willBeEnabled = !isNil(newIsEnabled)
        ? newIsEnabled
        : schedule.isEnabled;

      const startParsed = parse(
        effectiveStartTime,
        'HH:mm:ss',
        new Date()
      );

      let stopParsed = effectiveStopTime
        ? parse(effectiveStopTime, 'HH:mm:ss', new Date())
        : null;

      if (Number.isNaN(startParsed.getTime())) {
        return throwDetailedError({
          detailed: `Invalid time range. Start time ${effectiveStartTime}`,
          type: ErrorType.INVALID_SCHEDULE_SESSION_TIME_RANGE,
        });
      }

      if (stopParsed && Number.isNaN(stopParsed.getTime())) {
        return throwDetailedError({
          detailed: `Invalid time range. Stop time ${effectiveStopTime}`,
          type: ErrorType.INVALID_SCHEDULE_SESSION_TIME_RANGE,
        });
      }

      if (stopParsed && isBefore(stopParsed, startParsed)) {
        stopParsed = addDays(stopParsed, 1);
      }

      if (effectiveStopTime && effectiveStartTime === effectiveStopTime) {
        return throwDetailedError({
          detailed: `Invalid time range. Start time ${startParsed}, Stop time ${stopParsed}`,
          type: ErrorType.SCHEDULE_SAME_START_STOP_TIMES,
        });
      }

      const hasConflictingOtherSchedules = this.checkIsScheduleInConflict(
        schedule.id,
        startParsed,
        stopParsed,
        effectiveDays
      );

      // check for overlap / conflicts
      if (hasConflictingOtherSchedules) {
        return throwDetailedError({
          detailed: `Conflict. ID ${schedule.id}`,
          type: ErrorType.SCHEDULE_OVERLAP,
        });
      }

      if (!effectiveDays.length) {
        return throwDetailedError({
          detailed: `Missing days. ID ${schedule.id}`,
          type: ErrorType.SCHEDULE_NO_SELECTED_DAYS,
        });
      }

      const isStayAwake = isNumber((await getUserConfig()).stayAwake);
      const daysChanged = hasNewDays && !isEqual(effectiveDays, schedule.days);

      if (daysChanged) {
        schedule.days = effectiveDays;
      }

      if (
        (hasNewStartTime && effectiveStartTime !== schedule.startTime) ||
        daysChanged
      ) {
        schedule.startJob?.stop();
        schedule.startTime = effectiveStartTime;
        schedule.startJob = this.createCronJob(
          this.scheduleStartAction,
          schedule.startTime,
          schedule.days,
          id
        );

        if (willBeEnabled && isStayAwake) {
          schedule.startJob.start();
        }
      }

      if (
        (hasNewStopTime && effectiveStopTime !== schedule.stopTime) ||
        (daysChanged && schedule.stopTime)
      ) {
        schedule.stopJob?.stop();
        schedule.stopTime = effectiveStopTime;

        schedule.stopJob = effectiveStopTime
          ? this.createCronJob(
              this.scheduleEndAction,
              schedule.stopTime as TimeFormat,
              schedule.days,
              id
            )
          : null;

        if (willBeEnabled && isStayAwake) {
          schedule.stopJob?.start();
        }
      }

      if (!isNil(newIsEnabled) && schedule.isEnabled !== newIsEnabled) {
        schedule.isEnabled = newIsEnabled;

        if (newIsEnabled && isStayAwake) {
          if (!schedule.startJob) {
            schedule.startJob = this.createCronJob(
              this.scheduleStartAction,
              schedule.startTime,
              schedule.days,
              id
            );
          }
          if (schedule.stopTime && !schedule.stopJob) {
            schedule.stopJob = this.createCronJob(
              this.scheduleEndAction,
              schedule.stopTime,
              schedule.days,
              id
            );
          }
          if (!schedule?.startJob?.running) {
            schedule?.startJob?.start();
          }
          if (!schedule.stopJob?.running) {
            schedule.stopJob?.start();
          }
        } else {
          if (schedule?.startJob?.running) {
            schedule.startJob.stop();
          }
          if (schedule.stopJob?.running) {
            schedule.stopJob?.stop();
          }
        }
      }

      // Save the updated schedule to the map
      this.schedules.set(id, schedule);

      // Save the updated schedules metadata to the database
      await this.saveSchedulesToDb();
    } else {
      return throwDetailedError({
        detailed: `Schedule with ID ${id} does not exist.`,
        type: ErrorType.GENERIC,
      });
    }
  }

  public async removeSchedule(id: string) {
    const schedule = this.schedules.get(id);
    if (schedule) {
      schedule.startJob?.stop();
      schedule.stopJob?.stop();
      this.schedules.delete(id);
      await this.saveSchedulesToDb();
    }
  }
}
