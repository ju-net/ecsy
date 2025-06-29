
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/manifoldco/promptui"
	"github.com/spf13/cobra"
)

var (
	profile string
	cluster string
	service string
	task    string
	command string
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "ecsy",
		Short: "ECS Command Execute utility with MFA support",
		RunE:  run,
	}

	rootCmd.Flags().StringVarP(&profile, "profile", "p", "", "AWS profile name")
	rootCmd.Flags().StringVarP(&cluster, "cluster", "c", "", "ECS cluster name")
	rootCmd.Flags().StringVarP(&service, "service", "s", "", "ECS service name")
	rootCmd.Flags().StringVarP(&task, "task", "t", "", "ECS task ID")
	rootCmd.Flags().StringVar(&command, "command", "/bin/sh", "Command to execute")

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	// Select AWS profile
	selectedProfile, err := selectProfile()
	if err != nil {
		return fmt.Errorf("failed to select profile: %w", err)
	}

	// Load AWS config
	cfg, err := loadAWSConfig(ctx, selectedProfile)
	if err != nil {
		return fmt.Errorf("failed to load AWS config: %w", err)
	}

	ecsClient := ecs.NewFromConfig(cfg)

	// Select cluster
	selectedCluster, err := selectCluster(ctx, ecsClient)
	if err != nil {
		// Check if error is due to MFA requirement
		if strings.Contains(err.Error(), "explicit deny") || strings.Contains(err.Error(), "AccessDenied") {
			fmt.Println("Access denied. Attempting MFA authentication...")
			
			// Reload config with MFA
			cfg, err = loadAWSConfigWithMFA(ctx, selectedProfile)
			if err != nil {
				return fmt.Errorf("failed to load AWS config with MFA: %w", err)
			}
			
			// Recreate ECS client with new config
			ecsClient = ecs.NewFromConfig(cfg)
			
			// Retry cluster selection
			selectedCluster, err = selectCluster(ctx, ecsClient)
			if err != nil {
				return fmt.Errorf("failed to select cluster after MFA: %w", err)
			}
		} else {
			return fmt.Errorf("failed to select cluster: %w", err)
		}
	}

	// Select service
	selectedService, err := selectService(ctx, ecsClient, selectedCluster)
	if err != nil {
		return fmt.Errorf("failed to select service: %w", err)
	}

	// Select task
	selectedTask, err := selectTask(ctx, ecsClient, selectedCluster, selectedService)
	if err != nil {
		return fmt.Errorf("failed to select task: %w", err)
	}

	// Execute command
	return executeCommand(ctx, cfg, selectedCluster, selectedTask)
}

func selectProfile() (string, error) {
	if profile != "" {
		return profile, nil
	}

	// Get AWS profiles from ~/.aws/config
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	configPath := fmt.Sprintf("%s/.aws/config", homeDir)
	content, err := os.ReadFile(configPath)
	if err != nil {
		return "", fmt.Errorf("failed to read AWS config: %w", err)
	}

	var profiles []string
	lines := strings.Split(string(content), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[profile ") && strings.HasSuffix(line, "]") {
			profile := strings.TrimPrefix(line, "[profile ")
			profile = strings.TrimSuffix(profile, "]")
			profiles = append(profiles, profile)
		} else if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") && line != "[default]" {
			continue
		} else if line == "[default]" {
			profiles = append(profiles, "default")
		}
	}

	if len(profiles) == 0 {
		return "", fmt.Errorf("no AWS profiles found")
	}

	prompt := promptui.Select{
		Label: "Select AWS Profile",
		Items: profiles,
	}

	_, result, err := prompt.Run()
	if err != nil {
		return "", err
	}

	return result, nil
}

func loadAWSConfig(ctx context.Context, profile string) (aws.Config, error) {
	// Simply load config with profile
	return config.LoadDefaultConfig(ctx,
		config.WithSharedConfigProfile(profile),
	)
}

func loadAWSConfigWithMFA(ctx context.Context, profile string) (aws.Config, error) {
	// Load config with profile
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithSharedConfigProfile(profile),
	)
	if err != nil {
		return aws.Config{}, err
	}

	// Get MFA information
	homeDir, _ := os.UserHomeDir()
	configPath := fmt.Sprintf("%s/.aws/config", homeDir)
	content, _ := os.ReadFile(configPath)
	
	var mfaSerial string
	var sourceProfile string
	lines := strings.Split(string(content), "\n")
	inProfile := false
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[profile "+profile+"]") || (profile == "default" && line == "[default]") {
			inProfile = true
			continue
		}
		if inProfile && strings.HasPrefix(line, "[") {
			break
		}
		if inProfile && strings.HasPrefix(line, "mfa_serial") {
			parts := strings.Split(line, "=")
			if len(parts) == 2 {
				mfaSerial = strings.TrimSpace(parts[1])
			}
		}
		if inProfile && strings.HasPrefix(line, "source_profile") {
			parts := strings.Split(line, "=")
			if len(parts) == 2 {
				sourceProfile = strings.TrimSpace(parts[1])
			}
		}
	}

	// If no MFA serial found, check credentials file
	if mfaSerial == "" {
		credsPath := fmt.Sprintf("%s/.aws/credentials", homeDir)
		credsContent, _ := os.ReadFile(credsPath)
		credsLines := strings.Split(string(credsContent), "\n")
		
		inCredProfile := false
		for _, line := range credsLines {
			line = strings.TrimSpace(line)
			if line == "["+profile+"]" {
				inCredProfile = true
				continue
			}
			if inCredProfile && strings.HasPrefix(line, "[") {
				break
			}
			if inCredProfile && strings.HasPrefix(line, "mfa_serial") {
				parts := strings.Split(line, "=")
				if len(parts) == 2 {
					mfaSerial = strings.TrimSpace(parts[1])
					break
				}
			}
		}
	}

	// If still no MFA serial, try to list MFA devices
	if mfaSerial == "" {
		mfaSerial, err = selectMFADevice(ctx, cfg)
		if err != nil {
			return aws.Config{}, fmt.Errorf("failed to select MFA device: %w", err)
		}
	}

	// If source_profile is set, load that profile's credentials
	if sourceProfile != "" {
		cfg, err = config.LoadDefaultConfig(ctx,
			config.WithSharedConfigProfile(sourceProfile),
		)
		if err != nil {
			return aws.Config{}, err
		}
	}

	// Get MFA code
	prompt := promptui.Prompt{
		Label: "Enter MFA Code",
	}
	mfaCode, err := prompt.Run()
	if err != nil {
		return aws.Config{}, err
	}

	// Get session token with MFA
	stsClient := sts.NewFromConfig(cfg)
	tokenOutput, err := stsClient.GetSessionToken(ctx, &sts.GetSessionTokenInput{
		SerialNumber: aws.String(mfaSerial),
		TokenCode:    aws.String(mfaCode),
	})
	if err != nil {
		return aws.Config{}, fmt.Errorf("failed to get session token: %w", err)
	}

	// Create new config with temporary credentials
	cfg.Credentials = aws.NewCredentialsCache(aws.CredentialsProviderFunc(func(ctx context.Context) (aws.Credentials, error) {
		return aws.Credentials{
			AccessKeyID:     *tokenOutput.Credentials.AccessKeyId,
			SecretAccessKey: *tokenOutput.Credentials.SecretAccessKey,
			SessionToken:    *tokenOutput.Credentials.SessionToken,
			CanExpire:       true,
			Expires:         *tokenOutput.Credentials.Expiration,
		}, nil
	}))

	return cfg, nil
}

func selectMFADevice(ctx context.Context, cfg aws.Config) (string, error) {
	// Create IAM client
	iamClient := iam.NewFromConfig(cfg)
	
	// List MFA devices for the current user
	listOutput, err := iamClient.ListMFADevices(ctx, &iam.ListMFADevicesInput{})
	if err != nil {
		// If listing fails, fall back to manual entry
		fmt.Printf("Unable to list MFA devices: %v\n", err)
		mfaPrompt := promptui.Prompt{
			Label: "Enter MFA Device ARN (e.g., arn:aws:iam::123456789012:mfa/username)",
		}
		return mfaPrompt.Run()
	}
	
	if len(listOutput.MFADevices) == 0 {
		// No MFA devices found, ask for manual entry
		fmt.Println("No MFA devices found for the current user.")
		mfaPrompt := promptui.Prompt{
			Label: "Enter MFA Device ARN (e.g., arn:aws:iam::123456789012:mfa/username)",
		}
		return mfaPrompt.Run()
	}
	
	// Create device options for selection
	type mfaDeviceItem struct {
		SerialNumber string
		UserName     string
		Label        string
	}
	
	var deviceItems []mfaDeviceItem
	var deviceLabels []string
	
	for _, device := range listOutput.MFADevices {
		serialNumber := ""
		if device.SerialNumber != nil {
			serialNumber = *device.SerialNumber
		}
		
		userName := ""
		if device.UserName != nil {
			userName = *device.UserName
		}
		
		// Extract device name from serial number for better display
		deviceName := serialNumber
		if strings.Contains(serialNumber, "/") {
			parts := strings.Split(serialNumber, "/")
			if len(parts) > 0 {
				deviceName = parts[len(parts)-1]
			}
		}
		
		label := fmt.Sprintf("%s (User: %s)", deviceName, userName)
		
		deviceItems = append(deviceItems, mfaDeviceItem{
			SerialNumber: serialNumber,
			UserName:     userName,
			Label:        label,
		})
		deviceLabels = append(deviceLabels, label)
	}
	
	// If only one device, use it automatically
	if len(deviceItems) == 1 {
		fmt.Printf("Using MFA device: %s\n", deviceItems[0].Label)
		return deviceItems[0].SerialNumber, nil
	}
	
	// Multiple devices, let user choose
	prompt := promptui.Select{
		Label: "Select MFA Device",
		Items: deviceLabels,
	}
	
	index, _, err := prompt.Run()
	if err != nil {
		return "", err
	}
	
	return deviceItems[index].SerialNumber, nil
}

func selectCluster(ctx context.Context, client *ecs.Client) (string, error) {
	if cluster != "" {
		return cluster, nil
	}

	// List clusters
	listOutput, err := client.ListClusters(ctx, &ecs.ListClustersInput{})
	if err != nil {
		return "", err
	}

	if len(listOutput.ClusterArns) == 0 {
		return "", fmt.Errorf("no clusters found")
	}

	// Extract cluster names
	var clusterNames []string
	for _, arn := range listOutput.ClusterArns {
		parts := strings.Split(arn, "/")
		if len(parts) > 0 {
			clusterNames = append(clusterNames, parts[len(parts)-1])
		}
	}

	prompt := promptui.Select{
		Label: "Select ECS Cluster",
		Items: clusterNames,
	}

	_, result, err := prompt.Run()
	if err != nil {
		return "", err
	}

	return result, nil
}

func selectService(ctx context.Context, client *ecs.Client, clusterName string) (string, error) {
	if service != "" {
		return service, nil
	}

	// List services
	var serviceArns []string
	var nextToken *string

	for {
		listOutput, err := client.ListServices(ctx, &ecs.ListServicesInput{
			Cluster:   aws.String(clusterName),
			NextToken: nextToken,
		})
		if err != nil {
			return "", err
		}

		serviceArns = append(serviceArns, listOutput.ServiceArns...)

		if listOutput.NextToken == nil {
			break
		}
		nextToken = listOutput.NextToken
	}

	if len(serviceArns) == 0 {
		return "", fmt.Errorf("no services found in cluster %s", clusterName)
	}

	// Extract service names
	var serviceNames []string
	for _, arn := range serviceArns {
		parts := strings.Split(arn, "/")
		if len(parts) > 0 {
			serviceNames = append(serviceNames, parts[len(parts)-1])
		}
	}

	prompt := promptui.Select{
		Label: "Select ECS Service",
		Items: serviceNames,
	}

	_, result, err := prompt.Run()
	if err != nil {
		return "", err
	}

	return result, nil
}

func selectTask(ctx context.Context, client *ecs.Client, clusterName, serviceName string) (string, error) {
	if task != "" {
		return task, nil
	}

	// List tasks for the service
	listOutput, err := client.ListTasks(ctx, &ecs.ListTasksInput{
		Cluster:     aws.String(clusterName),
		ServiceName: aws.String(serviceName),
	})
	if err != nil {
		return "", err
	}

	if len(listOutput.TaskArns) == 0 {
		return "", fmt.Errorf("no tasks found for service %s", serviceName)
	}

	// Describe tasks to get more details
	describeOutput, err := client.DescribeTasks(ctx, &ecs.DescribeTasksInput{
		Cluster: aws.String(clusterName),
		Tasks:   listOutput.TaskArns,
	})
	if err != nil {
		return "", err
	}

	// Create task items with more info
	type taskItem struct {
		ID     string
		Status string
		Label  string
	}

	var taskItems []taskItem
	for _, task := range describeOutput.Tasks {
		taskID := ""
		if task.TaskArn != nil {
			parts := strings.Split(*task.TaskArn, "/")
			if len(parts) > 0 {
				taskID = parts[len(parts)-1]
			}
		}

		status := ""
		if task.LastStatus != nil {
			status = *task.LastStatus
		}

		label := fmt.Sprintf("%s (%s)", taskID, status)
		taskItems = append(taskItems, taskItem{
			ID:     taskID,
			Status: status,
			Label:  label,
		})
	}

	// Filter only RUNNING tasks
	var runningTasks []string
	for _, item := range taskItems {
		if item.Status == "RUNNING" {
			runningTasks = append(runningTasks, item.Label)
		}
	}

	if len(runningTasks) == 0 {
		return "", fmt.Errorf("no running tasks found for service %s", serviceName)
	}

	prompt := promptui.Select{
		Label: "Select ECS Task",
		Items: runningTasks,
	}

	_, result, err := prompt.Run()
	if err != nil {
		return "", err
	}

	// Extract task ID from selection
	for _, item := range taskItems {
		if item.Label == result {
			return item.ID, nil
		}
	}

	return "", fmt.Errorf("task not found")
}

func executeCommand(ctx context.Context, cfg aws.Config, clusterName, taskID string) error {
	// Build aws ecs execute-command
	args := []string{
		"ecs", "execute-command",
		"--cluster", clusterName,
		"--task", taskID,
		"--interactive",
		"--command", command,
	}

	// Add region if available
	if cfg.Region != "" {
		args = append(args, "--region", cfg.Region)
	}

	// Use AWS CLI with the session credentials
	cmd := exec.Command("aws", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Set environment variables for AWS credentials
	creds, err := cfg.Credentials.Retrieve(ctx)
	if err != nil {
		return fmt.Errorf("failed to retrieve credentials: %w", err)
	}

	cmd.Env = append(os.Environ(),
		fmt.Sprintf("AWS_ACCESS_KEY_ID=%s", creds.AccessKeyID),
		fmt.Sprintf("AWS_SECRET_ACCESS_KEY=%s", creds.SecretAccessKey),
	)
	if creds.SessionToken != "" {
		cmd.Env = append(cmd.Env, fmt.Sprintf("AWS_SESSION_TOKEN=%s", creds.SessionToken))
	}

	fmt.Printf("Executing command on task %s...\n", taskID)
	return cmd.Run()
}