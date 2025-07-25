
package main

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/manifoldco/promptui"
	"github.com/spf13/cobra"
)

var (
	// Version information (set by ldflags)
	version = "dev"
	
	// Command flags
	profile   string
	cluster   string
	service   string
	task      string
	command   string
	container string
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
	rootCmd.Flags().StringVar(&container, "container", "", "Container name to execute command in")

	// Add version command
	versionCmd := &cobra.Command{
		Use:   "version",
		Short: "Show version information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("ecsy version %s\n", version)
		},
	}
	rootCmd.AddCommand(versionCmd)

	// Add update command
	updateCmd := &cobra.Command{
		Use:   "update",
		Short: "Check for updates and install the latest version",
		RunE:  checkAndUpdate,
	}
	rootCmd.AddCommand(updateCmd)

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
	return executeCommand(ctx, cfg, selectedCluster, selectedTask, selectedService)
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
		// No running tasks, ask if user wants to start a new one
		fmt.Printf("No running tasks found for service %s.\n", serviceName)
		
		prompt := promptui.Prompt{
			Label:     "Would you like to start a new task",
			IsConfirm: true,
		}
		
		_, err := prompt.Run()
		if err != nil {
			return "", fmt.Errorf("no running tasks found for service %s", serviceName)
		}
		
		// Start a new task
		fmt.Println("Starting a new task...")
		newTaskID, err := startNewTask(ctx, client, clusterName, serviceName)
		if err != nil {
			return "", fmt.Errorf("failed to start new task: %w", err)
		}
		
		return newTaskID, nil
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

func selectContainer(ctx context.Context, client *ecs.Client, clusterName, taskID string) (string, error) {
	// Describe task to get container details
	describeOutput, err := client.DescribeTasks(ctx, &ecs.DescribeTasksInput{
		Cluster: aws.String(clusterName),
		Tasks:   []string{taskID},
	})
	if err != nil {
		return "", err
	}

	if len(describeOutput.Tasks) == 0 {
		return "", fmt.Errorf("task not found: %s", taskID)
	}

	task := describeOutput.Tasks[0]
	
	// Get container names from task
	var containerNames []string
	for _, container := range task.Containers {
		if container.Name != nil {
			containerNames = append(containerNames, *container.Name)
		}
	}

	if len(containerNames) == 0 {
		return "", fmt.Errorf("no containers found in task %s", taskID)
	}

	// If only one container, use it automatically
	if len(containerNames) == 1 {
		return containerNames[0], nil
	}

	// Multiple containers, let user choose
	prompt := promptui.Select{
		Label: "Select Container",
		Items: containerNames,
	}

	_, result, err := prompt.Run()
	if err != nil {
		return "", err
	}

	return result, nil
}

func executeCommand(ctx context.Context, cfg aws.Config, clusterName, taskID, serviceName string) error {
	// Select container if not specified
	selectedContainer := container
	if selectedContainer == "" {
		ecsClient := ecs.NewFromConfig(cfg)
		var err error
		selectedContainer, err = selectContainer(ctx, ecsClient, clusterName, taskID)
		if err != nil {
			return fmt.Errorf("failed to select container: %w", err)
		}
	}

	// Build aws ecs execute-command
	args := []string{
		"ecs", "execute-command",
		"--cluster", clusterName,
		"--task", taskID,
		"--interactive",
		"--command", command,
	}

	// Add container if specified
	if selectedContainer != "" {
		args = append(args, "--container", selectedContainer)
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

// GitHub release structure
type GitHubRelease struct {
	TagName string `json:"tag_name"`
	Name    string `json:"name"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

func checkAndUpdate(cmd *cobra.Command, args []string) error {
	fmt.Println("Checking for updates...")

	// Get latest release from GitHub
	resp, err := http.Get("https://api.github.com/repos/ju-net/ecsy/releases/latest")
	if err != nil {
		return fmt.Errorf("failed to check for updates: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to get latest release: status %d", resp.StatusCode)
	}

	var release GitHubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return fmt.Errorf("failed to parse release info: %w", err)
	}

	latestVersion := strings.TrimPrefix(release.TagName, "v")
	currentVersion := strings.TrimPrefix(version, "v")

	if currentVersion == "dev" {
		fmt.Println("Running development version. Latest release is", latestVersion)
	} else if latestVersion == currentVersion {
		fmt.Printf("You are already running the latest version (%s)\n", version)
		return nil
	} else {
		fmt.Printf("Current version: %s\n", version)
		fmt.Printf("Latest version: %s\n", latestVersion)
	}

	// Ask for confirmation
	prompt := promptui.Prompt{
		Label:     fmt.Sprintf("Do you want to update to version %s", latestVersion),
		IsConfirm: true,
	}

	_, err = prompt.Run()
	if err != nil {
		fmt.Println("Update cancelled")
		return nil
	}

	// Determine the asset name based on the current platform
	assetName := getAssetName()
	
	// Find the download URL
	var downloadURL string
	for _, asset := range release.Assets {
		if asset.Name == assetName {
			downloadURL = asset.BrowserDownloadURL
			break
		}
	}

	if downloadURL == "" {
		return fmt.Errorf("no release found for platform %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	// Download and install
	fmt.Printf("Downloading %s...\n", assetName)
	if err := downloadAndInstall(downloadURL); err != nil {
		return fmt.Errorf("failed to install update: %w", err)
	}

	fmt.Println("Update completed successfully!")
	fmt.Printf("ecsy has been updated to version %s\n", latestVersion)
	return nil
}

func getAssetName() string {
	osName := runtime.GOOS
	arch := runtime.GOARCH
	
	// Map to release asset names
	if osName == "darwin" && arch == "amd64" {
		return "ecsy-darwin-amd64.gz"
	} else if osName == "darwin" && arch == "arm64" {
		return "ecsy-darwin-arm64.gz"
	} else if osName == "linux" && arch == "amd64" {
		return "ecsy-linux-amd64.gz"
	} else if osName == "linux" && arch == "arm64" {
		return "ecsy-linux-arm64.gz"
	} else if osName == "windows" && arch == "amd64" {
		return "ecsy-windows-amd64.exe.gz"
	}
	
	return fmt.Sprintf("ecsy-%s-%s.gz", osName, arch)
}

func downloadAndInstall(url string) error {
	// Get the executable path
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}

	// Download the file
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: status %d", resp.StatusCode)
	}

	// Create temporary file
	tmpFile, err := os.CreateTemp("", "ecsy-update-*.gz")
	if err != nil {
		return err
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	// Download to temporary file
	if _, err := io.Copy(tmpFile, resp.Body); err != nil {
		return err
	}

	// Close the file to ensure all data is written
	tmpFile.Close()

	// Decompress the file
	decompressedFile := strings.TrimSuffix(tmpFile.Name(), ".gz")
	if err := decompressGzip(tmpFile.Name(), decompressedFile); err != nil {
		return fmt.Errorf("failed to decompress: %w", err)
	}
	defer os.Remove(decompressedFile)

	// Make the new binary executable
	if err := os.Chmod(decompressedFile, 0755); err != nil {
		return err
	}

	// Check if we need sudo (write permission to the directory)
	execDir := strings.TrimSuffix(execPath, "/"+filepath.Base(execPath))
	if !isWritable(execDir) {
		fmt.Println("Update requires administrative privileges...")
		return installWithSudo(decompressedFile, execPath)
	}

	// Backup current executable
	backupPath := execPath + ".backup"
	if err := os.Rename(execPath, backupPath); err != nil {
		return fmt.Errorf("failed to backup current executable: %w", err)
	}

	// Move new executable to the original path
	if err := os.Rename(decompressedFile, execPath); err != nil {
		// Try to restore backup
		os.Rename(backupPath, execPath)
		return fmt.Errorf("failed to install new executable: %w", err)
	}

	// Remove backup
	os.Remove(backupPath)

	return nil
}

func decompressGzip(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	gzReader, err := gzip.NewReader(srcFile)
	if err != nil {
		return err
	}
	defer gzReader.Close()

	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	if _, err := io.Copy(dstFile, gzReader); err != nil {
		return err
	}

	return nil
}

func isWritable(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	
	// Check if we can write to the directory
	testFile := filepath.Join(path, ".ecsy-write-test")
	file, err := os.Create(testFile)
	if err != nil {
		return false
	}
	file.Close()
	os.Remove(testFile)
	
	return info.Mode().Perm()&0200 != 0
}

func installWithSudo(src, dst string) error {
	// Create backup of current executable
	backupPath := dst + ".backup"
	
	// Use sudo to backup current executable
	backupCmd := exec.Command("sudo", "cp", dst, backupPath)
	if err := backupCmd.Run(); err != nil {
		// Ignore error if file doesn't exist (first install)
		if !strings.Contains(err.Error(), "No such file") {
			return fmt.Errorf("failed to backup with sudo: %w", err)
		}
	}
	
	// Use sudo to move new executable
	moveCmd := exec.Command("sudo", "mv", src, dst)
	moveCmd.Stdout = os.Stdout
	moveCmd.Stderr = os.Stderr
	if err := moveCmd.Run(); err != nil {
		// Try to restore backup
		if backupPath != "" {
			restoreCmd := exec.Command("sudo", "mv", backupPath, dst)
			restoreCmd.Run()
		}
		return fmt.Errorf("failed to install with sudo: %w", err)
	}
	
	// Remove backup
	if backupPath != "" {
		removeCmd := exec.Command("sudo", "rm", "-f", backupPath)
		removeCmd.Run()
	}
	
	return nil
}

func startNewTask(ctx context.Context, client *ecs.Client, clusterName, serviceName string) (string, error) {
	// Get service details to find task definition
	describeOutput, err := client.DescribeServices(ctx, &ecs.DescribeServicesInput{
		Cluster:  aws.String(clusterName),
		Services: []string{serviceName},
	})
	if err != nil {
		return "", fmt.Errorf("failed to describe service: %w", err)
	}

	if len(describeOutput.Services) == 0 {
		return "", fmt.Errorf("service not found: %s", serviceName)
	}

	service := describeOutput.Services[0]
	if service.TaskDefinition == nil {
		return "", fmt.Errorf("no task definition found for service %s", serviceName)
	}

	// Run a new task
	runTaskOutput, err := client.RunTask(ctx, &ecs.RunTaskInput{
		Cluster:        aws.String(clusterName),
		TaskDefinition: service.TaskDefinition,
		LaunchType:     service.LaunchType,
		NetworkConfiguration: service.NetworkConfiguration,
		PlatformVersion: service.PlatformVersion,
		EnableExecuteCommand: true,
	})
	if err != nil {
		return "", fmt.Errorf("failed to run task: %w", err)
	}

	if len(runTaskOutput.Tasks) == 0 {
		return "", fmt.Errorf("no task was created")
	}

	newTask := runTaskOutput.Tasks[0]
	taskID := ""
	if newTask.TaskArn != nil {
		parts := strings.Split(*newTask.TaskArn, "/")
		if len(parts) > 0 {
			taskID = parts[len(parts)-1]
		}
	}

	fmt.Printf("New task started: %s\n", taskID)
	fmt.Println("Waiting for task to become running...")

	// Wait for task to be in RUNNING state
	waiter := ecs.NewTasksRunningWaiter(client)
	err = waiter.Wait(ctx, &ecs.DescribeTasksInput{
		Cluster: aws.String(clusterName),
		Tasks:   []string{taskID},
	}, 2*time.Minute)
	
	if err != nil {
		return "", fmt.Errorf("task failed to start: %w", err)
	}

	fmt.Println("Task is now running!")
	return taskID, nil
}