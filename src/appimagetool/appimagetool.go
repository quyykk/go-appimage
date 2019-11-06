package main

import (

	"flag"
	"fmt"
	"github.com/agriardyan/go-zsyncmake/zsync"
	"github.com/probonopd/appimage/internal/helpers"
	"gopkg.in/ini.v1"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// https://blog.kowalczyk.info/article/vEja/embedding-build-number-in-go-executable.html
// The build script needs to set, e.g.,
// go build -ldflags "-X main.commit=$TRAVIS_BUILD_NUMBER"
var commit string

var flgVersion bool

func main() {

	// Parse command line arguments
	flag.BoolVar(&flgVersion, "version", false, "Show version number")
	flag.Parse()

	// Always show version, but exit immediately if only the version number was requested
	if commit != "" {
		fmt.Printf("%s %s\n", filepath.Base(os.Args[0]), commit)
	} else {
		fmt.Println("Unsupported local", filepath.Base(os.Args[0]), "developer build")
	}
	if flgVersion == true {
		os.Exit(0)
	}

	// Add the location of the executable to the $PATH
	helpers.AddHereToPath()

	// Check for needed files on $PATH
	tools := []string{"file", "mksquashfs", "desktop-file-validate"}
	for _, t := range tools {
		if helpers.IsCommandAvailable(t) == false {
			fmt.Println("Required helper tool", t, "missing")
			os.Exit(1)
		}
	}

	// Check whether we have a sufficient version of mksquashfs for -offset
	if helpers.CheckIfSquashfsVersionSufficient("mksquashfs") == false {
		os.Exit(1)
	}

	// Check if first argument is present, exit otherwise
	if len(os.Args) < 2 {
		os.Stderr.WriteString("Please specify an AppDir to be converted to an AppImage \n")
		os.Exit(1)
	}

	// Check if is directory, then assume we want to convert an AppDir into an AppImage
	firstArg, _ := filepath.EvalSymlinks(os.Args[1])
	if info, err := os.Stat(firstArg); err == nil && info.IsDir() {
		GenerateAppImage(firstArg)
	} else {
		// TODO: If it is a file, then check if it is an AppImage and if yes, extract it
		os.Stderr.WriteString("Supplied argument is not a directory \n")
		os.Exit(1)
	}
}

// GenerateAppImage converts an AppDir into an AppImage
func GenerateAppImage(appdir string) {

	// Guess update information
	// Check if $VERSION is empty and git is on the path, if yes "git rev-parse --short HEAD"
	version := ""
	version = os.Getenv("VERSION")
	if version == "" && helpers.IsCommandAvailable("git") == true {
		version, err := exec.Command("git", "rev-parse", "--short", "HEAD", appdir).Output()
		os.Stderr.WriteString("Could not determine version automatically, please supply the application version as $VERSION " + filepath.Base(os.Args[0]) + " ... \n")
		os.Exit(1) ////////////// Temporarily disabled for debugging
		if err == nil {
			fmt.Println("NOTE: Using", version, "from 'git rev-parse --short HEAD' as the version")
			fmt.Println("      Please set the $VERSION environment variable if this is not intended")
		}
	}

	// Check if *.desktop file is present in source AppDir
	// find_first_matching_file_nonrecursive(source, "*.desktop");

	// If no desktop file found, exit
	n := len(helpers.FilesWithSuffixInDirectory(appdir, ".desktop"))
	if n < 1 {
		os.Stderr.WriteString("No top-level desktop file found in " + appdir + ", aborting\n")
		os.Exit(1)
	}

	// If more than one desktop files found, exit
	if n > 1 {
		os.Stderr.WriteString("Multiple top-level desktop files found in" + appdir + ", aborting\n")
		os.Exit(1)
	}

	desktopfile := helpers.FilesWithSuffixInDirectory(appdir, ".desktop")[0]

	err := helpers.ValidateDesktopFile(desktopfile)
	helpers.PrintError("ValidateDesktopFile", err)
	if err != nil {
		os.Exit(1)
	}

	// Read information from .desktop file

	// Check for presence of "Categories=" key and abort otherwise
	d, err := ini.Load(desktopfile)
	helpers.PrintError("ini.load", err)
	neededKeys := []string{"Categories", "Name", "Exec", "Type", "Icon"}
	for _, k := range neededKeys {
		if d.Section("Desktop Entry").HasKey(k) == false {
			os.Stderr.WriteString(".desktop file is missing a '" + k + "'= key\n")
			os.Exit(1)
		}
	}

	val, _ := d.Section("Desktop Entry").GetKey("Icon")
	iconname := val.String()
	if strings.Contains(iconname, "/") {
		os.Stderr.WriteString("Desktop file contains Icon= entry with a path, aborting\n")
		os.Exit(1)
	}

	if strings.Contains(filepath.Base(iconname), ".") {
		os.Stderr.WriteString("Desktop file contains Icon= entry with '.', aborting\n")
		os.Exit(1)
	}

	// Read "Name=" key and convert spaces into underscores
	val, _ = d.Section("Desktop Entry").GetKey("Name")
	name := strings.Replace(val.String(), " ", "_", 999)
	fmt.Println(name)

	// Determine the architecture
	// If no $ARCH variable is set check all .so that we can find to determine the architecture
	var archs []string
	if os.Getenv("ARCH") == "" {
		res, err := helpers.GetElfArchitecture(appdir+"/AppRun")
		if err == nil {
			archs = helpers.AppendIfMissing(archs, res)
			fmt.Println("Architecture from AppRun:", res)
		} else {
		err := filepath.Walk(appdir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				helpers.PrintError("Determine architecture", err)
			} else if info.IsDir() == false && strings.Contains(info.Name(), ".so.") {
				arch, err := helpers.GetElfArchitecture(path)
				helpers.PrintError("Determine architecture", err)
				fmt.Println("Architecture of", info.Name(), arch)
				archs = helpers.AppendIfMissing(archs, arch)
			}
			return nil
		})
		helpers.PrintError("Determine architecture", err)
		}
	} else {
		archs = helpers.AppendIfMissing(archs, os.Getenv("ARCH"))
			fmt.Println("Architecture from $ARCH:", os.Getenv("ARCH"))
	}

	if len(archs) != 1 {
		os.Stderr.WriteString("Could not determine architecture automatically, please supply it as $ARCH " + filepath.Base(os.Args[0]) + " ... \n")
		os.Exit(1)
	}
	arch := archs[0]

	// Set VERSION in desktop file and save it
	d, err = ini.Load(desktopfile)
	ini.PrettyFormat = false
	helpers.PrintError("ini.load", err)
	d.Section("Desktop Entry").Key("X-AppImage-Version").SetValue(version)
	err = d.SaveTo(desktopfile)
	helpers.PrintError("Save desktop file", err)

	// Construct target AppImage filename
	target := name + "-" + version + "-" + arch + ".AppImage"
	fmt.Println(target)

	var iconfile string

	// Check if we find a png matching the Icon= key in the top-level directory of the AppDir
	// We insist on a png because otherwise we need to costly convert it to png at integration time
	// since thumbails need to be in png format
	if helpers.CheckIfFileExists(appdir+"/"+iconname+".png") == true {
		iconfile = appdir + "/" + iconname + ".png"
	} else {
		os.Stderr.WriteString("Could not find icon file at " + appdir + "/" + iconname + ".png" + ", exiting\n")
		fmt.Println("TODO: As a fallback, search in usr/share/icons/hicolor/256x256 and copy from there")
		os.Exit(1)
	}
	fmt.Println(iconfile)

	fmt.Println("TODO: Check validity and size of png")

	// "Deleting pre-existing .DirIcon"
	if helpers.CheckIfFileExists(appdir+"/.DirIcon") == true {
		fmt.Println("Deleting pre-existing .DirIcon")
		os.Remove(appdir + "/.DirIcon")
	}

	// "Copying .DirIcon in place based on information from desktop file"
	err = helpers.CopyFile(iconfile, appdir+"/.DirIcon")
	if err != nil {
		helpers.PrintError("Copy .DirIcon", err)
		os.Exit(1)
	}

	// Check if AppStream upstream metadata is present in source AppDir
	// If yes, use ximion's appstreamcli to make sure that desktop file and appdata match together and are valid
	appstreamfile := appdir + "/usr/share/metainfo/" + filepath.Base(desktopfile) + ".appdata.xml"
	if helpers.CheckIfFileExists(appstreamfile) == false {
		fmt.Println("WARNING: AppStream upstream metadata is missing, please consider creating it in")
		fmt.Println("         " + appdir + "/usr/share/metainfo/" + filepath.Base(desktopfile) + ".appdata.xml")
		fmt.Println("         Please see https://www.freedesktop.org/software/appstream/docs/chap-Quickstart.html#sect-Quickstart-DesktopApps")
		fmt.Println("         for more information or use the generator at")
		fmt.Println("         http://output.jsbin.com/qoqukof")
	} else {
		fmt.Println("Trying to validate AppStream information with the appstreamcli tool")
			if helpers.IsCommandAvailable("appstreamcli") == false {
				fmt.Println("Required helper tool appstreamcli missing")
				os.Exit(1)
		}
		err = helpers.ValidateAppStreamMetainfoFile(appdir)
		if err != nil {
			fmt.Println("In case of questions regarding the validation, please refer to https://github.com/ximion/appstream")
			os.Exit(1)
		}
	}

	runtimedir := filepath.Clean(helpers.Here() + "/../share/AppImageKit/runtime/")
	if _, err := os.Stat(runtimedir); os.IsNotExist(err) {
		runtimedir = helpers.Here()
	}
	runtimefilepath := runtimedir + "/runtime-" + arch
	if helpers.CheckIfFileExists(runtimefilepath) == false {
		os.Stderr.WriteString("Cannot find " + runtimefilepath + ", exiting\n")
		fmt.Println("It should have been bundled, but you can get it from https://github.com/AppImage/AppImageKit/releases/continuous")
		// TODO: Download it from there?
		os.Exit(1)
	}

	// Find out the size of the binary runtime
	fi, err := os.Stat(runtimefilepath)
	if err != nil {
		helpers.PrintError("runtime", err)
		os.Exit(1)
	}
	offset := fi.Size()

	// "mksquashfs", source, destination, "-offset", offset, "-comp", "gzip", "-root-owned", "-noappend"
	cmd := exec.Command("mksquashfs", appdir, target, "-offset", strconv.FormatInt(offset, 10), "-comp", "gzip", "-root-owned", "-noappend")
	fmt.Println(cmd.String())
	out, err := cmd.CombinedOutput()
	if err != nil {
		helpers.PrintError("mksquashfs", err)
		fmt.Printf("%s", string(out))
		os.Exit(1)
	}

	// Embed the binary runtime into the squashfs
	fmt.Println("Embedding ELF...")

	err = helpers.WriteFileIntoOtherFileAtOffset(runtimefilepath, target, 0)
	if err != nil {
		helpers.PrintError("Embedding runtime", err)
		fmt.Printf("%s", string(out))
		os.Exit(1)
	}

	fmt.Println("Marking the AppImage as executable...")
	os.Chmod(target, 0755)

	// Construct update information
	var updateinformation string

	// If we know this is a Travis CI build,
	// then fill in update information based on TRAVIS_REPO_SLUG
	//     https://docs.travis-ci.com/user/environment-variables/#Default-Environment-Variables
	//     TRAVIS_COMMIT: The commit that the current build is testing.
	//     TRAVIS_REPO_SLUG: The slug (in form: owner_name/repo_name) of the repository currently being built.
	//     TRAVIS_TAG: If the current build is for a git tag, this variable is set to the tag’s name.
	//     TRAVIS_PULL_REQUEST
	if os.Getenv("TRAVIS_REPO_SLUG") != "" {
		fmt.Println("Running on Travis CI")
		if os.Getenv("TRAVIS_PULL_REQUEST") != "false" {
			fmt.Println("Will not calculate update information for GitHub because this is a pull request")
		} else if os.Getenv("GITHUB_TOKEN") == "" {
			fmt.Println("Will not calculate update information for GitHub because $GITHUB_TOKEN is missing")
			fmt.Println("please set it in the Travis CI Repository Settings for this project.")
			fmt.Println("You can get one from https://github.com/settings/tokens")
		} else {
			parts := strings.Split(os.Getenv("TRAVIS_REPO_SLUG"), "/")
			channel := "continuous" // FIXME: Under what circumstances do we need "latest" here?
			updateinformation = "gh-releases-zsync|" + parts[0] + "|" + parts[1] + "|" + channel + "|" + name + "-" + "*-" + arch + ".AppImage.zsync"
			fmt.Println("Calculated updateinformation:", updateinformation)
		}
	}

	// If we know this is a GitLab CI build
	// do nothing at the moment but print some nice message
	// https://docs.gitlab.com/ee/ci/variables/#predefined-variables-environment-variables
	// CI_PROJECT_URL
	// "CI_COMMIT_REF_NAME"); The branch or tag name for which project is built
	// "CI_JOB_NAME"); The name of the job as defined in .gitlab-ci.yml
	if os.Getenv("CI_COMMIT_REF_NAME") != "" {
		fmt.Println("Running on GitLab CI")
		fmt.Println("Will not calculate update information for GitLab because GitLab does not support HTTP range requests yet")
	}

	// TODO: If updateinformation was provided, then we check and embed it
	// but questionable whether we should have users do this since it is complex and prone to error
	// if(!g_str_has_prefix(updateinformation,"zsync|"))
	// if(!g_str_has_prefix(updateinformation,"bintray-zsync|"))
	// if(!g_str_has_prefix(updateinformation,"gh-releases-zsync|"))
	// die("The provided updateinformation is not in a recognized format");

	// Find offset and length of updateinformation
	uidata, err := helpers.GetSectionData(target, ".upd_info")
	helpers.PrintError("GetSectionData for '.upd_info'", err)
	if err != nil {
		os.Stderr.WriteString("Could not find section .upd_info in runtime, exiting\n")
		os.Exit(1)
	}
	fmt.Println("Embedded .upd-info section before embedding updateinformation:")
	fmt.Println(uidata)

	uioffset, uilength, err := helpers.GetSectionOffsetAndLength(target, ".upd_info")
	helpers.PrintError("GetSectionData for '.upd_info'", err)
	if err != nil {
		os.Stderr.WriteString("Could not determine offset and length of .upd_info in runtime, exiting\n")
		os.Exit(1)
	}
	fmt.Println("Embedded .upd-info section length:", uioffset)
	fmt.Println("Embedded .upd-info section length:", uilength)

	// Exit if updateinformation exceeds available space
	if len(updateinformation) > len(uidata) {
		os.Stderr.WriteString("updateinformation does not fit into .upd_info segment, exiting\n")
		os.Exit(1)
	}

	fmt.Println("Writing updateinformation into .upd_info segment...", uilength)

	// Seek file to ui_offset and write it there
	helpers.WriteStringIntoOtherFileAtOffset(updateinformation,target,uioffset)
	helpers.PrintError("GetSectionData for '.upd_info'", err)
	if err != nil {
		os.Stderr.WriteString("Could write into .upd_info segment, exiting\n")
		os.Exit(1)
	}

	uidata, err = helpers.GetSectionData(target, ".upd_info")
	helpers.PrintError("GetSectionData for '.upd_info'", err)
	if err != nil {
		os.Stderr.WriteString("Could not find section .upd_info in runtime, exiting\n")
		os.Exit(1)
	}
	fmt.Println("Embedded .upd-info section:", string(uidata))

	// TODO: calculate and embed MD5 digest
	// https://github.com/AppImage/AppImageKit/blob/801e789390d0e6848aef4a5802cd52da7f4abafb/src/appimagetool.c#L961
	// Blocked by https://github.com/AppImage/AppImageSpec/issues/29

	// TODO: Signing. It is pretty convoluted and hardly anyone is using it.
	//  Can we make it much simpler to use? Check how goreleaser does it.

	// If updateinformation was provided, then we also generate the zsync file (after having signed the AppImage)
	if updateinformation != "" {
		opts := zsync.Options{0, "", filepath.Base(target)}
		zsync.ZsyncMake(target, opts)
	}

	fmt.Println("Success")
	fmt.Println("")
	fmt.Println("Please consider submitting your AppImage to AppImageHub, the crowd-sourced")
	fmt.Println("central directory of available AppImages, by opening a pull request")
	fmt.Println("at https://github.com/AppImage/appimage.github.io")
}
