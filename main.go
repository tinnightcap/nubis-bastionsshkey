package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
)

func IgnoreUserLDAPUserObjects(s []LDAPUserObject, e string) bool {
	for _, a := range s {
		if a.Uid == e {
			return true
		}
	}
	return false
}
func IgnoreUser(s []string, e string) bool {
	for _, a := range s {
		if a == e {
			return true
		}
	}
	return false
}
func TrimSuffix(s, suffix string) string {
	if strings.HasSuffix(s, suffix) {
		s = s[:len(s)-len(suffix)]
	}
	return s
}
func SyncLDAPToConsul(userClass string, usersSet []LDAPUserObject, noop bool, c *ConsulClient, conf Configuration) {
	namespace := fmt.Sprintf("%s/%s/", conf.Consul.Namespace, userClass)
	globalAdminsConsul, _, _ := c.client.Keys(namespace, "/", nil)
	for _, consulAdminUser := range globalAdminsConsul {
		keyPath := consulAdminUser
		consulAdminUser = TrimSuffix(consulAdminUser, "/")
		usernameSplit := strings.Split(consulAdminUser, "/")
		consulAdminUsername := usernameSplit[len(usernameSplit)-1]
		ignoreConsulUser := IgnoreUserLDAPUserObjects(usersSet, consulAdminUsername)
		if consulAdminUsername == userClass {
			continue
		}
		if ignoreConsulUser == false {
			if noop == false {
				log.Printf("Removing %s from %s", consulAdminUsername, userClass)
				log.Printf("KeyPath %s", keyPath)
				c.client.DeleteTree(keyPath, nil)
			} else {
				log.Printf("Should remove %s from %s", consulAdminUsername, userClass)
			}
		}

	}
}
func main() {
	var configFilePath string
	var execType string
	var testDestEmail string
	// @TODO: Temporary variable to be removed after confident user creation is being handled correctly
	testUserName := ""
	flag.StringVar(&testUserName, "testUserName", "", "Test UserName for creating a user. Will be removed, for debugging only.")
	userCreationPath := ""
	flag.StringVar(&userCreationPath, "userCreationPath", "", "Test userCreationPath for creating a user. Will be removed, for debugging only.")
	var noop bool
	flag.StringVar(&configFilePath, "c", "", "Configuration file to use")
	flag.StringVar(&execType, "execType", "consul", "consul|IAM\nUse consul to sync LDAP to consul, use IAM to sync IAM users from LDAP")
	flag.StringVar(&testDestEmail, "testDestEmail", "", "Email Address for testing email")

	// dynamoDB flags
	var useDynamo bool
	var region, key, environment, accountName, service, unicredsPath, consulPort, consulDomain string
	flag.BoolVar(&useDynamo, "useDynamo", false, "Bool to use dynamodb for config file")
	flag.StringVar(&region, "region", "", "dynamoDB Region")
	flag.StringVar(&key, "key", "", "dynamoDB Region")
	flag.StringVar(&environment, "environment", "", "dynamoDB Region")
	flag.StringVar(&service, "service", "", "dynamoDB Region")
	flag.StringVar(&accountName, "accountName", "", "accountName")
	flag.StringVar(&consulPort, "consulPort", "8500", "Consul port to connect to")
	flag.StringVar(&consulDomain, "consulDomain", "localhost.localdomain", "Domain of the consul server")
	// end dynamoDB flags
	flag.BoolVar(&noop, "noop", false, "noop - providing noop makes functionality displayed without taking any action")
	flag.Parse()
	if configFilePath != "" && useDynamo != false {
		log.Fatal("Incorrect flags. dynamoDBPath and configFilePath cannot both be provided.")
	}

	d := ConfigOptions{}
	log.Println(os.Args[1:])
	if useDynamo == true {
		if region == "" {
			log.Fatal("-region is required when using dynamoDBPath")
		}
		if key == "" {
			log.Fatal("-key is required when using dynamoDBPath")
		}
		if accountName == "" {
			log.Fatal("-accountName is required when using dynamoDBPath")
		}
		if environment == "" {
			log.Fatal("-environment is required when using dynamoDBPath")
		}
		if service == "" {
			log.Fatal("-service is required when using dynamoDBPath")
		}
		if unicredsPath == "" {
			unicredsPath = "./unicreds"
		}
		d.Region = region
		d.Environment = environment
		d.Service = service
		d.AccountName = accountName
		d.Key = key
		d.UseDynamo = true
		d.UnicredsPath = "./unicreds"
		d.ConsulPort = consulPort
		d.ConsulDomain = consulDomain
		d.ConsulServer = d.DeriveConsulServer()
	}
	if useDynamo == false && configFilePath == "" {
		d.ConfigFilePath = "config.yml"
		d.UseDynamo = false
	}
	configuration, err := getConfig(d)
	if err != nil {
		log.Fatal("Unable to read configuration")
	}
	configValid, configError := validateConfig(configuration)
	if configValid == false {
		fmt.Println(configError)
		os.Exit(2)
	}
	c := GetConsulClient(configuration)
	var usersSet []string
	var allLDAPGroupUserObjects []LDAPUserObject
	var allEntries []ConsulEntries
	for _, x := range configuration.LdapServer.IAMGroupMapping {
		tmpGroupMembers := getGroupMembers(configuration, x)
		for _, user := range tmpGroupMembers {
			usersSet = append(usersSet, user.Uid)
			allLDAPGroupUserObjects = append(allLDAPGroupUserObjects, user)
		}
		tmp := ConsulEntries{tmpGroupMembers, x}
		allEntries = append(allEntries, tmp)
	}
	usersSet = SortUsers(usersSet)

	if execType == "consul" {
		for _, g_entry := range allEntries {
			for _, entry := range g_entry.Users {
				if noop == false {
					c.Put(entry, configuration, g_entry.Group.ConsulPath)
				} else {
					fmt.Println(entry.Uid)
				}
			}
			SyncLDAPToConsul(g_entry.Group.ConsulPath, g_entry.Users, noop, c, configuration)
		}
	} else if execType == "IAM" {
		IAMUsers, IAMUsersErr := GetAllIAMUsers(configuration)
		if IAMUsersErr != nil {
			log.Println(IAMUsersErr)
		}
		if IAMUsersErr == nil {
			iamUsers := []string{}
			for _, user := range IAMUsers.Users {
				username := *user.UserName
				path := *user.Path
				ignoreUser := IgnoreUser(configuration.AWS.AWSIgnoreUserList, username)
				ignorePath := IgnoreUser(configuration.AWS.AWSIgnorePathList, path)
				if ignoreUser == false && ignorePath == false {
					iamUsers = append(iamUsers, username)
					if noop {
						log.Printf("Acting upon user: %s", username)
					}
				} else {
					if noop {
						log.Printf("Ignoring user: %s", username)
					}
				}
			}
			for _, user := range iamUsers {
				if IgnoreUser(usersSet, user) == false {
					if noop {
						log.Printf("User: %s doesn't exist in usersSet", user)
					} else {
						log.Printf("Removing: %s from iamUsers", user)

					}
				}
			}
			for _, user := range usersSet {
				if IgnoreUser(iamUsers, user) == false {
					if noop {
						log.Printf("User: %s doesn't exist in iamUsers", user)
					} else {
						path := userCreationPath
						if user == testUserName {
							userRet, err := CreateIAMUser(configuration, user, path)
							if err != nil {
								log.Fatal(err)
							}
							userLDAPObj, found := GetLDAPUserObjectFromGroup(user, allLDAPGroupUserObjects)
							if found == false {
								fmt.Println("Here we need to log/track that people don't have a PGPPublicKey in LDAP")
							}
							emailBody := []byte(fmt.Sprintf("AccessKey: %s\nSecretKey: %s", userRet.AccessKey, userRet.SecretKey))
							testEncrypted, encryptErr := EncryptMailBody(emailBody, userLDAPObj.PGPPublicKey, testDestEmail)
							if encryptErr != nil {
								log.Fatal(encryptErr)
							}
							SendWelcomeMail(configuration, testDestEmail, testEncrypted)

						}
						log.Printf("Adding: %s to iamUsers", user)
					}
				}
			}
		}
	}
}
