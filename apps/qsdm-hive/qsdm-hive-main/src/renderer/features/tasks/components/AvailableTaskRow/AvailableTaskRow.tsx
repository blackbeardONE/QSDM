import {
  Icon,
  PauseFill,
  PlayFill,
  WarningCircleLine,
} from 'vendor/qsdm-styleguide';
import { useAutoAnimate } from '@formkit/auto-animate/react';
import React, {
  memo,
  MouseEventHandler,
  MutableRefObject,
  ReactNode,
  RefObject,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react';
import { useQuery, useQueryClient } from 'react-query';
import { useNavigate } from 'react-router-dom';
import { twMerge } from 'tailwind-merge';

import GearFill from 'assets/svgs/gear-fill.svg';
import GearLine from 'assets/svgs/gear-line.svg';
import {
  BONUS_TASK_DEPLOYER,
  BONUS_TASK_NAME,
  CRITICAL_MAIN_ACCOUNT_BALANCE,
} from 'config/node';
import { NATIVE_TOKEN_SYMBOL } from 'config/nativeToken';
import { QSDM_TASK_RUNTIME_MODE } from 'config/qsdm';
import { isQsdmSystemTaskId } from 'config/qsdmSystemTasks';
import { KPLBalanceResponse, RequirementTag, RequirementType } from 'models';
import {
  Button,
  ColumnsLayout,
  EditStakeInput,
  LoadingSpinner,
  LoadingSpinnerSize,
  TableRow,
} from 'renderer/components/ui';
import { DROPDOWN_MENU_ID } from 'renderer/components/ui/Dropdown/Dropdown';
import { Popover } from 'renderer/components/ui/Popover/Popover';
import { trackEvent } from 'renderer/services/analytics';
import {
  StakingErrors,
  useAccountBalance,
  useAddTaskVariableModal,
  useAllStoredPairedTaskVariables,
  useConfirmRunTask,
  useFundNewAccountModal,
  useMainAccount,
  useMetadata,
  useOnClickOutside,
  useStakingAccountBalance,
  useStartingTasksContext,
  useUserAppConfig,
} from 'renderer/features';
import { useAutoPairVariables } from 'renderer/features/common/hooks/useAutoPairVariables';
import { useBonusTask } from 'renderer/features/common/hooks/useBonusTask';
import { useCreateMissingKPLStakingKey } from 'renderer/features/shared/hooks/useCreateMissingKPLStakingKey';
import { useKplToken } from 'renderer/features/tokens/hooks/useKplToken';
import {
  getQsdmCoreStatus,
  getKPLStakingAccountPubKey,
  QueryKeys,
  stopTask,
} from 'renderer/services';
import { Task } from 'renderer/types';
import { Theme } from 'renderer/types/common';
import { AppRoute } from 'renderer/types/routes';
import { getCellFromBaseUnits, getBaseUnitsFromCell } from 'utils';

import { useMyTaskStake } from '../../hooks/useMyTaskStake';
import { useTaskStatsData } from '../../hooks/useTaskStatsData';
import { isNetworkingTask } from '../../utils';
import { SuccessMessage } from '../AvailableTasksTable/components/SuccessMessage';
import { RoundTime } from '../common/RoundTime';
import { CurrencyDisplay } from '../MyNodeTaskRow/components/CurrencyDisplay';
import { Tags } from '../MyNodeTaskRow/components/Tags';
import { Thumbnail } from '../MyNodeTaskRow/components/Thumbnail';
import { Token } from '../MyNodeTaskRow/components/Token';
import { TaskInfo, TaskStatsDataType } from '../TaskInfo';
import { TaskSettings } from '../TaskSettings';

import { FailedMessage } from './components/FailedMessage';
import { useTaskThumbnail } from './useTaskThumbnail';

const compactQsdmAddress = (address?: string) =>
  address && address.length > 20
    ? `${address.slice(0, 8)}...${address.slice(-6)}`
    : address || '';

const normalizeQsdmStakeToBaseUnits = (stake: number) =>
  stake > 0 && stake < 1_000_000 ? getBaseUnitsFromCell(stake) : stake;

interface Props {
  task: Task;
  columnsLayout: ColumnsLayout;
  isPrivate: boolean;
}

function AvailableTaskRow({ task, columnsLayout, isPrivate }: Props) {
  const [isAddTaskSettingModalOpen, setIsAddTaskSettingModalOpen] =
    useState<boolean>(false);
  const { showModal: showAddTaskVariableModal } = useAddTaskVariableModal();
  const { taskName, publicKey, isRunning, roundTime, stakePotAccount } = task;
  const isQsdmNativeSystemTask =
    QSDM_TASK_RUNTIME_MODE === 'qsdm-native' && isQsdmSystemTaskId(publicKey);
  const queryCache = useQueryClient();
  const [accordionView, setAccordionView] = useState<
    'info' | 'settings' | null
  >(null);
  const [isTaskToolsValid, setIsTaskToolsValid] = useState(false);
  const [isTaskValidToRun, setIsTaskValidToRun] = useState(false);
  const [taskStartSucceeded, setTaskStartSucceeded] = useState(false);
  const [taskStartFailedWithError, setTaskStartFailedWithError] = useState<
    string | null
  >(null);
  const [valueToStake, setValueToStake] = useState<number>(
    task.minimumStakeAmount || 0
  );
  const [errorMessage, setErrorMessage] = useState<string | ReactNode>('');
  const [shouldBounce, setShouldBounce] = useState(true);

  const { executeTask, getTaskStartPromise, getIsTaskLoading } =
    useStartingTasksContext();
  const { data: kplStakingKey, error: kplStakingKeyError } = useQuery(
    QueryKeys.KPLStakingAccount,
    getKPLStakingAccountPubKey,
    {
      retry: false,
    }
  );
  const { data: qsdmCoreStatus } = useQuery(
    [QueryKeys.QsdmCoreStatus, 'task-stake-owner'],
    getQsdmCoreStatus,
    {
      enabled: QSDM_TASK_RUNTIME_MODE === 'qsdm-native',
      staleTime: 5000,
      refetchInterval: 15000,
    }
  );

  const isMissingKplStakingKey =
    task.taskType === 'KPL' && !kplStakingKey && !!kplStakingKeyError;
  const isBonusTask =
    taskName === BONUS_TASK_NAME && task.taskManager === BONUS_TASK_DEPLOYER;

  const { data: mainAccountPubKey = '' } = useMainAccount();
  const { accountBalance = 0 } = useAccountBalance(mainAccountPubKey);
  const { accountBalance: stakingAccountBalance = 0 } =
    useStakingAccountBalance();

  const {
    storedPairedTaskVariablesQuery: { data: allPairedVariables = {} },
  } = useAllStoredPairedTaskVariables({
    enabled: !!publicKey,
  });

  const pairedVariables = Object.entries(allPairedVariables).filter(
    ([taskId]) => taskId === publicKey
  )[0]?.[1];

  const ref = useRef<HTMLDivElement>(null);

  const parent = useAutoAnimate();

  const closeAccordionView = useCallback(() => {
    if (isAddTaskSettingModalOpen) {
      return;
    }
    setAccordionView(null);
  }, [isAddTaskSettingModalOpen]);

  useOnClickOutside(
    ref as MutableRefObject<HTMLDivElement>,
    closeAccordionView,
    DROPDOWN_MENU_ID
  );

  const {
    data: alreadyStakedTokensAmount = 0,
    isLoading: loadingCurrentTaskStake,
  } = useMyTaskStake(task.publicKey, task.taskType, false);
  const qsdmSigner = qsdmCoreStatus?.taskSigner?.sender || '';
  const foreignQsdmStakeOwners = useMemo(() => {
    if (
      QSDM_TASK_RUNTIME_MODE !== 'qsdm-native' ||
      !qsdmSigner ||
      alreadyStakedTokensAmount !== 0
    ) {
      return [];
    }

    return Object.entries(task.stakeList || {})
      .filter(
        ([owner, stake]) =>
          owner.toLowerCase() !== qsdmSigner.toLowerCase() &&
          Number(stake) > 0
      )
      .map(([owner]) => owner);
  }, [alreadyStakedTokensAmount, qsdmSigner, task.stakeList]);
  const hasStakeOnAnotherQsdmSigner = foreignQsdmStakeOwners.length > 0;

  const handleToggleView = useCallback(
    (view: 'info' | 'settings') => {
      const newView = accordionView === view ? null : view;
      setAccordionView(newView);
    },
    [accordionView]
  );

  const handleTaskToolsValidationCheck = (isValid: boolean) => {
    setIsTaskToolsValid(isValid);
  };
  const { topStake, totalBounty, totalNodes, totalStake, lastReward } =
    useTaskStatsData(publicKey);
  const qsdmNativeStakeStats = useMemo(() => {
    const stakes = Object.values(task.stakeList || {})
      .map((stake) => normalizeQsdmStakeToBaseUnits(Number(stake) || 0))
      .filter((stake) => stake > 0);

    return {
      topStake: stakes.length ? Math.max(...stakes) : 0,
      totalStake: stakes.reduce((total, stake) => total + stake, 0),
      totalNodes: stakes.length,
    };
  }, [task.stakeList]);

  const isEditStakeInputDisabled =
    alreadyStakedTokensAmount !== 0 || loadingCurrentTaskStake;

  const { metadata, isLoadingMetadata } = useMetadata({
    metadataCID: task.metadataCID,
    taskPublicKey: publicKey,
  });

  const globalAndTaskVariables: RequirementTag[] = useMemo(
    () => {
      if (isQsdmNativeSystemTask) {
        return [];
      }

      return (
        metadata?.requirementsTags?.filter(({ type }) =>
          [
            RequirementType.TASK_VARIABLE,
            RequirementType.GLOBAL_VARIABLE,
          ].includes(type)
        ) || []
      );
    },
    [isQsdmNativeSystemTask, metadata?.requirementsTags]
  );

  useAutoPairVariables({
    taskPublicKey: publicKey,
    taskVariables: globalAndTaskVariables,
  });

  const { taskThumbnail } = useTaskThumbnail({
    thumbnailUrl: metadata?.imageUrl,
  });

  useEffect(() => {
    getTaskStartPromise(publicKey)
      ?.then(() => {
        setTaskStartSucceeded(true);
        setTaskStartFailedWithError(null);
      })
      .catch((e) => {
        setTaskStartSucceeded(false);
        setTaskStartFailedWithError(e.message);
      });
  }, [getTaskStartPromise, publicKey, task.publicKey, taskName]);

  useEffect(() => {
    const validateAllVariablesWerePaired = () => {
      const numberOfPairedVariables = Object.keys(pairedVariables || {}).length;

      const allVariablesWerePaired =
        (globalAndTaskVariables?.length || 0) === numberOfPairedVariables ||
        globalAndTaskVariables?.length === 0;
      // setIsGlobalToolsValid(allVariablesWerePaired);
      setIsTaskToolsValid(allVariablesWerePaired);
    };

    validateAllVariablesWerePaired();
  }, [pairedVariables, globalAndTaskVariables]);

  const { kplToken, isLoading: isLoadingKplToken } = useKplToken({
    tokenType: task.tokenType || '',
  });
  const tokenTicker = kplToken?.symbol || NATIVE_TOKEN_SYMBOL;
  const minStakeToDisplay =
    task.taskType === 'KPL'
      ? task.minimumStakeAmount / 10 ** (kplToken?.decimals || 9)
      : getCellFromBaseUnits(task.minimumStakeAmount);

  // TO DO KPL: handle KPL when converting units
  const details: TaskStatsDataType = useMemo(
    () => ({
      nodesNumber: isQsdmNativeSystemTask
        ? qsdmNativeStakeStats.totalNodes
        : totalNodes,
      minStake: task.minimumStakeAmount,
      myStake: getCellFromBaseUnits(alreadyStakedTokensAmount),
      topStake: getCellFromBaseUnits(
        isQsdmNativeSystemTask ? qsdmNativeStakeStats.topStake : topStake
      ),
      bounty: getCellFromBaseUnits(totalBounty),
      totalStakeInCell: getCellFromBaseUnits(
        isQsdmNativeSystemTask ? qsdmNativeStakeStats.totalStake : totalStake
      ),
      roundTime,
    }),
    [
      totalNodes,
      isQsdmNativeSystemTask,
      qsdmNativeStakeStats.totalNodes,
      qsdmNativeStakeStats.topStake,
      qsdmNativeStakeStats.totalStake,
      task.minimumStakeAmount,
      alreadyStakedTokensAmount,
      topStake,
      totalBounty,
      totalStake,
      roundTime,
    ]
  );

  const { userConfig: settings, isUserConfigLoading: loadingSettings } =
    useUserAppConfig();

  const isUsingNetworking = useMemo(
    () => isNetworkingTask(metadata),
    [metadata]
  );

  const userHasNetworkingEnabled = useMemo(
    () => settings?.networkingFeaturesEnabled,
    [settings]
  );
  const playButtonIsDisabled =
    (!isRunning && !isTaskValidToRun) ||
    loadingSettings ||
    // isMissingKplStakingKey ||
    (isUsingNetworking && !userHasNetworkingEnabled);

  const navigate = useNavigate();

  const { showModal: showFundModal } = useFundNewAccountModal({});

  const kplBalanceListQueryData: KPLBalanceResponse[] =
    queryCache.getQueryData([QueryKeys.KplBalanceList, mainAccountPubKey]) ||
    [];
  const kplBalance =
    kplBalanceListQueryData?.find((balance) => balance.mint === task.tokenType)
      ?.balance || 0;
  const hasEnoughBalanceForStaking =
    alreadyStakedTokensAmount || task.taskType === 'CELL'
      ? accountBalance >= valueToStake
      : kplBalance >= valueToStake;
  const hasEnoughNativeTokens =
    task.taskType === 'KPL' || alreadyStakedTokensAmount
      ? accountBalance >= CRITICAL_MAIN_ACCOUNT_BALANCE
      : accountBalance >= valueToStake + CRITICAL_MAIN_ACCOUNT_BALANCE;

  const meetsMinimumStake = useMemo(
    () =>
      !!alreadyStakedTokensAmount || valueToStake >= task.minimumStakeAmount,
    [alreadyStakedTokensAmount, valueToStake, task.minimumStakeAmount]
  );

  const validateTask = useCallback(() => {
    const isTaskValid =
      hasEnoughBalanceForStaking && hasEnoughNativeTokens && isTaskToolsValid;
    setIsTaskValidToRun(isTaskValid);

    const getErrorMessage = () => {
      if (isRunning) return '';

      const conditions = [
        {
          condition: hasEnoughBalanceForStaking,
          errorMessage: `have enough ${tokenTicker} to stake`,
          action: task.taskType === 'CELL' ? showFundModal : undefined,
        },
        {
          condition: hasEnoughNativeTokens,
          errorMessage: `have enough ${NATIVE_TOKEN_SYMBOL} for fees`,
          action: showFundModal,
        },
        {
          condition: meetsMinimumStake,
          errorMessage: hasStakeOnAnotherQsdmSigner
            ? `stake this Task with the current QSDM signer. Existing stake belongs to ${compactQsdmAddress(
                foreignQsdmStakeOwners[0]
              )}; current signer is ${compactQsdmAddress(qsdmSigner)}`
            : `stake at least ${minStakeToDisplay} ${tokenTicker} on this Task`,
        },
        {
          condition: isTaskToolsValid,
          errorMessage: 'configure the Task extensions',
          action: () => setAccordionView('settings'),
        },
        {
          condition:
            !isUsingNetworking ||
            (isUsingNetworking && userHasNetworkingEnabled),
          errorMessage: "enable Networking in your node's settings",
          action: () => navigate(AppRoute.SettingsNetworkAndUPNP),
        },
      ];

      const errors = conditions
        .filter(({ condition }) => !condition)
        .map(({ errorMessage, action }) => ({ errorMessage, action }));

      if (errors.length === 0) {
        return '';
      } else if (errors.length === 1) {
        return (
          <div>
            Make sure you{' '}
            {errors[0].action ? (
              <button
                onClick={errors[0].action}
                className="font-semibold text-finnieTeal-100 hover:underline"
              >
                {errors[0].errorMessage}.
              </button>
            ) : (
              <>{errors[0].errorMessage}.</>
            )}
          </div>
        );
      } else {
        const errorList = errors.map(({ errorMessage, action }) => (
          <li key={errorMessage}>
            •{' '}
            {action ? (
              <button
                onClick={action}
                className="font-semibold text-finnieTeal-100 hover:underline"
              >
                {errorMessage}
              </button>
            ) : (
              errorMessage
            )}
          </li>
        ));
        return (
          <div>
            Make sure you:
            <br />
            <ul> {errorList}</ul>
          </div>
        );
      }
    };

    const errorMessage = getErrorMessage();
    setErrorMessage(errorMessage);
  }, [
    hasEnoughBalanceForStaking,
    isTaskToolsValid,
    hasEnoughNativeTokens,
    meetsMinimumStake,
    hasStakeOnAnotherQsdmSigner,
    foreignQsdmStakeOwners,
    qsdmSigner,
    tokenTicker,
    isRunning,
    showFundModal,
    minStakeToDisplay,
    isUsingNetworking,
    userHasNetworkingEnabled,
    handleToggleView,
    navigate,
    // isMissingKplStakingKey,
    // showCreateMissingKplStakingKeyModalMemo,
  ]);

  useEffect(() => {
    validateTask();
  }, [validateTask]);

  const handleStopTask = async () => {
    try {
      await stopTask(publicKey);
    } catch (error) {
      console.error(error);
    } finally {
      queryCache.invalidateQueries();
    }
  };

  const settingsViewIsOpen = accordionView === 'settings';
  const shouldNotShowSettingsView = !globalAndTaskVariables?.length;

  const handleStakeValueChange = (value: number) => {
    if (value) {
      // open task details if user is trying to stake if not open
      if (!settingsViewIsOpen && !shouldNotShowSettingsView) {
        handleToggleView('settings');
      }
    }
    setValueToStake(value);
  };

  const handleOpenAddTaskVariableModal = useCallback(
    (dropdownRef: RefObject<HTMLButtonElement>, tool: string) => {
      setIsAddTaskSettingModalOpen((prev) => !prev);
      showAddTaskVariableModal(tool).then((wasVariableAdded) => {
        setIsAddTaskSettingModalOpen(false);
        // focus dropdown after creating new variable
        if (wasVariableAdded) {
          setTimeout(() => {
            dropdownRef?.current?.click();
          }, 100);
        }
      });
    },
    [showAddTaskVariableModal]
  );

  const getTaskDetailsComponent = useCallback(() => {
    if (
      (accordionView === 'info' || accordionView === 'settings') &&
      isLoadingMetadata
    ) {
      return <LoadingSpinner />;
    }

    if (accordionView === 'info') {
      return (
        <TaskInfo
          publicKey={task.publicKey}
          metadataCID={task.metadataCID}
          metadata={metadata ?? undefined}
          details={details}
          creator={task.taskManager}
          tokenTicker={tokenTicker}
        />
      );
    }

    if (accordionView === 'settings') {
      return (
        <TaskSettings
          taskPubKey={task.publicKey}
          onToolsValidation={handleTaskToolsValidationCheck}
          taskVariables={globalAndTaskVariables}
          onPairingSuccess={closeAccordionView}
          onOpenAddTaskVariableModal={handleOpenAddTaskVariableModal}
          moveToTaskInfo={() => handleToggleView('info')}
        />
      );
    }

    return null;
  }, [
    accordionView,
    isLoadingMetadata,
    task.publicKey,
    task.metadataCID,
    task.taskManager,
    metadata,
    details,
    globalAndTaskVariables,
    closeAccordionView,
    handleOpenAddTaskVariableModal,
    handleToggleView,
    tokenTicker,
  ]);

  useBonusTask(isBonusTask, () => {
    if (isTaskValidToRun) {
      showConfirmRunTaskModal();
    } else if (!isTaskToolsValid) {
      handleToggleView('settings');
    } else {
      handleToggleView('info');
    }
  });

  const GearIcon = globalAndTaskVariables?.length ? GearFill : GearLine;
  const gearIconColor = `hover:rotate-180 transition-all duration-300 ease-in-out ${
    isTaskToolsValid ? 'text-finnieEmerald-light' : 'text-[#FFA6A6]'
  }`;
  const gearTooltipContent = !globalAndTaskVariables?.length
    ? "This task doesn't use any task extensions"
    : isTaskToolsValid
    ? 'Open task extensions'
    : 'You need to set up the Task extensions first in order to run this Task.';
  const qsdmStakeOwnerTooltip = hasStakeOnAnotherQsdmSigner
    ? `Existing stake is bound to ${compactQsdmAddress(
        foreignQsdmStakeOwners[0]
      )}. Starting with ${compactQsdmAddress(
        qsdmSigner
      )} will add a separate one-time stake for the current signer.`
    : '';
  const runButtonTooltipContent =
    errorMessage ||
    qsdmStakeOwnerTooltip ||
    (isRunning ? 'Stop task' : 'Start task');

  const taskStartCompleted = useMemo(() => {
    if (taskStartFailedWithError === StakingErrors.STAKING_CANCELLED_BY_USER) {
      return null;
    }

    if (!taskStartSucceeded && !taskStartFailedWithError) {
      return null;
    }

    if (taskStartSucceeded) {
      return <SuccessMessage />;
    }

    return (
      <FailedMessage
        runTaskAgain={() => {
          executeTask({
            publicKey,
            valueToStake,
            alreadyStakedTokensAmount,
            isUsingNetworking,
            stakePotAccount,
            isPrivate: !task.isWhitelisted,
            taskType: task.taskType,
            mintAddress: task.tokenType,
          });
          setTaskStartFailedWithError(null);
        }}
      />
    );
  }, [
    taskStartFailedWithError,
    taskStartSucceeded,
    executeTask,
    publicKey,
    valueToStake,
    alreadyStakedTokensAmount,
    isUsingNetworking,
    stakePotAccount,
    task.isWhitelisted,
    task.taskType,
    task.tokenType,
  ]);

  const tableRowClasses = twMerge(
    'py-2 gap-y-0 cursor-pointer group/row',
    isBonusTask
      ? 'bg-gradient-45 bg-[length:200%_100%] animate-gold-glimmer'
      : ''
  );

  const runTask = () => {
    executeTask({
      publicKey,
      valueToStake,
      alreadyStakedTokensAmount,
      isUsingNetworking,
      stakePotAccount,
      taskType: task.taskType,
      mintAddress: task.tokenType,
      isPrivate: !task.isWhitelisted,
    });
    trackEvent('task_start', {
      taskName,
      taskPublicKey: publicKey,
      valueToStake,
    });
  };

  const { showModal: showConfirmRunTaskModal } = useConfirmRunTask({
    taskName: task.taskName,
    pairedVariables,
    stake: alreadyStakedTokensAmount || valueToStake,
    onConfirm: runTask,
    ticker: kplToken?.symbol,
    decimals: kplToken?.decimals,
    isPrivate,
    taskId: publicKey,
  });

  const { showModal: showCreateMissingKplStakingKeyModal } =
    useCreateMissingKPLStakingKey();

  const showCreateMissingKplStakingKeyModalMemo = useCallback(
    () =>
      showCreateMissingKplStakingKeyModal().then(() => {
        return showConfirmRunTaskModal();
      }),
    [showConfirmRunTaskModal]
  );

  const handleToggleInfoAccordion: MouseEventHandler = (event) => {
    if (event.target !== event.currentTarget) {
      return;
    }
    event.stopPropagation();
    handleToggleView('info');
  };

  const handleToggleInfoAccordionOnKeyDown = (event: React.KeyboardEvent) => {
    if (event.key === 'Enter' || event.key === ' ') {
      setAccordionView((currentAccordionView) =>
        currentAccordionView === 'info' ? null : 'info'
      );
    }
  };

  const tags = (metadata?.tags || []).filter((tag) => tag !== 'ORCA_TASK');

  return (
    taskStartCompleted || (
      <TableRow
        columnsLayout={columnsLayout}
        className={tableRowClasses}
        ref={ref}
        onClick={handleToggleInfoAccordion}
      >
        <Thumbnail
          taskName={taskName}
          tooltipContent={
            accordionView === 'info'
              ? 'Close task details'
              : 'Open task details'
          }
          onKeyDown={handleToggleInfoAccordionOnKeyDown}
          onClick={handleToggleInfoAccordion}
          src={taskThumbnail}
          taskId={publicKey}
        />

        <Tags
          isLoadingTags={isLoadingMetadata}
          tags={tags}
          isPrivate={isPrivate}
        />

        <div className="flex justify-between bg-finnieBlue-light-transparent  h-[56px] xl:w-[220px] rounded-lg p-2 px-4 gap-8 cursor-default">
          <div className="flex flex-col justify-between">
            <RoundTime roundTimeInMs={task.roundTime} />
          </div>
          <Popover
            theme={Theme.Dark}
            tooltipContent="Latest reward issued by this task to each node running it"
          >
            <div className="flex flex-col justify-between w-max">
              <div className="text-[10px] ">Last Reward</div>
              <div className="text-[12px] mx-auto w-fit">
                {lastReward ? (
                  <CurrencyDisplay
                    amount={lastReward}
                    currency={tokenTicker}
                    precision={2}
                  />
                ) : (
                  'N/A'
                )}
              </div>
            </div>
          </Popover>
        </div>

        <Token token={kplToken} isLoading={isLoadingKplToken} />

        <EditStakeInput
          meetsMinimumStake={meetsMinimumStake}
          stake={alreadyStakedTokensAmount || valueToStake}
          minStake={minStakeToDisplay}
          onChange={handleStakeValueChange}
          disabled={isEditStakeInputDisabled}
          tokenTicker={tokenTicker}
        />

        <Popover tooltipContent={gearTooltipContent} theme={Theme.Dark}>
          <div className="flex flex-col items-center justify-start w-10">
            <Button
              onMouseDown={() => handleToggleView('settings')}
              disabled={shouldNotShowSettingsView}
              icon={
                <Icon source={GearIcon} size={44} className={gearIconColor} />
              }
              onlyIcon
            />
          </div>
        </Popover>

        {getIsTaskLoading(publicKey) ? (
          <div className="py-[0.72rem]">
            <LoadingSpinner size={LoadingSpinnerSize.Large} />
          </div>
        ) : (
          <Popover theme={Theme.Dark} tooltipContent={runButtonTooltipContent}>
            <div className="relative group/play">
              {!!(errorMessage || qsdmStakeOwnerTooltip) && (
                <WarningCircleLine className="absolute -left-4 text-gray-500" />
              )}
              <div onMouseEnter={() => setShouldBounce(false)}>
                <Button
                  onlyIcon
                  icon={
                    <Icon
                      source={isRunning ? PauseFill : PlayFill}
                      size={22}
                      className={`w-full flex my-4 ${
                        shouldBounce && !settings?.hasRunFirstTask
                          ? 'animate-bounce'
                          : ''
                      } ${
                        isTaskValidToRun || isRunning
                          ? 'text-finnieTeal group-hover/play:text-finnieTeal-100 group-hover/play:scale-110 transition-all duration-300 ease-in-out'
                          : 'text-gray-500 cursor-not-allowed'
                      }`}
                    />
                  }
                  onClick={
                    isRunning
                      ? handleStopTask
                      : isMissingKplStakingKey
                      ? showCreateMissingKplStakingKeyModalMemo
                      : showConfirmRunTaskModal
                  }
                  disabled={playButtonIsDisabled || loadingCurrentTaskStake}
                />
              </div>
            </div>
          </Popover>
        )}

        <div
          className={`w-full col-span-9 overflow-y-auto ${
            accordionView !== null
              ? // 9000px is just a simbolic value of a ridiculously high height, the animation needs absolute max-h values to work properly (fit/max/etc won't work)
                'opacity-1 pb-3 pt-5 max-h-[9000px]'
              : 'opacity-0 max-h-0'
          } transition-all duration-500 ease-in-out`}
        >
          <div ref={parent} className="flex w-full">
            {getTaskDetailsComponent()}
          </div>
        </div>
      </TableRow>
    )
  );
}

export default memo(AvailableTaskRow);
